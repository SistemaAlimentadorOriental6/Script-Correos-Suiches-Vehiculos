package main

import (
	"crypto/md5"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

// ConectarBD inicializa y verifica la conexión a la base de datos MySQL.
func ConectarBD(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("error al abrir la base de datos: %w", err)
	}

	// Verificar si la conexión está viva
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("error al hacer ping a la base de datos: %w", err)
	}

	return db, nil
}

// ResolverBusID busca el UUID (bus_id) en la tabla `buses` por el nombre del carro.
// Si ya existe un registro para este bus en el mismo mes y año actual (en hora de Colombia), lo retorna.
// Si no existe para el mes actual, creará un nuevo registro en la tabla `buses`.
func ResolverBusID(db *sql.DB, carro string) (string, error) {
	if carro == "" {
		return "", fmt.Errorf("el número de carro está vacío")
	}

	locCol := time.FixedZone("America/Bogota", -5*60*60)
	ahoraCol := time.Now().In(locCol)

	// Consultar todos los registros del bus ordenados por fecha de creación descendente
	query := "SELECT id, fecha_creacion FROM buses WHERE codigo = ? ORDER BY fecha_creacion DESC"
	rows, err := db.Query(query, carro)
	if err != nil {
		return "", fmt.Errorf("error al consultar el bus en la base de datos: %w", err)
	}
	defer rows.Close()

	var busIDExistente string
	encontradoEnMesActual := false

	for rows.Next() {
		var id string
		var fecha sql.NullTime
		if err := rows.Scan(&id, &fecha); err != nil {
			return "", fmt.Errorf("error al leer datos del bus: %w", err)
		}
		if fecha.Valid {
			fechaCol := fecha.Time.In(locCol)
			// Verificar si corresponde al mismo año y mes actual
			if fechaCol.Year() == ahoraCol.Year() && fechaCol.Month() == ahoraCol.Month() {
				busIDExistente = id
				encontradoEnMesActual = true
				break
			}
		}
	}

	if encontradoEnMesActual {
		return busIDExistente, nil
	}

	// Si no se encontró ningún registro para el mes actual, creamos uno nuevo
	log.Printf("El bus '%s' no tiene registro para el mes %d/%d en la base de datos. Creando registro en la tabla 'buses'...\n", carro, ahoraCol.Month(), ahoraCol.Year())
	nuevoID := uuid.New().String()
	
	queryInsert := "INSERT INTO buses (id, codigo, fecha_creacion) VALUES (?, ?, ?)"
	_, errInsert := db.Exec(queryInsert, nuevoID, carro, ahoraCol)
	if errInsert != nil {
		return "", fmt.Errorf("error al crear el registro del bus en la tabla 'buses' automáticamente: %w", errInsert)
	}
	
	return nuevoID, nil
}

// GuardarReporteBateria extrae los datos del texto del reporte e inserta la información en registros_bateria.
func GuardarReporteBateria(db *sql.DB, textoFormateado string, correo Correo, messageID string) error {
	// Convertir la fecha de la prueba a la zona horaria de Colombia (UTC-5)
	locCol := time.FixedZone("America/Bogota", -5*60*60)
	fechaPruebaCol := correo.Fecha.In(locCol)

	reporte := ParsearTextoAReporte(textoFormateado)
	reporte.FechaRegistro = fechaPruebaCol
	if messageID == "" {
		// Si el Message-ID original es vacío, generamos uno determinista único basado en metadatos y adjuntos
		var infoAdjuntos string
		for _, adj := range correo.Adjuntos {
			infoAdjuntos += fmt.Sprintf("-%s-%d", adj.Nombre, len(adj.Contenido))
		}
		datosUnicos := fmt.Sprintf("%s-%s-%s%s", correo.De, correo.Fecha.Format(time.RFC3339), correo.Asunto, infoAdjuntos)
		reporte.MessageID = fmt.Sprintf("HASH-%x", md5.Sum([]byte(datosUnicos)))
	} else {
		reporte.MessageID = messageID
	}

	// Si el número de batería es el por defecto (BATERÍA 1), intentar extraer del Asunto o Cuerpo del correo
	if reporte.Bateria == "BATERÍA 1" {
		textoAdicional := correo.Asunto + " " + correo.Cuerpo
		if bat := extraerNumeroBateria(textoAdicional); bat != "" {
			reporte.Bateria = bat
		}
	}

	// Si el número de bus está vacío, intentar extraer del Asunto o Cuerpo del correo
	if reporte.BusText == "" || reporte.BusText == "BUS" {
		textoAdicional := correo.Asunto + " " + correo.Cuerpo
		if bus := extraerNumeroBus(textoAdicional); bus != "" {
			reporte.BusText = bus
		}
	}

	// Resolver el bus_id (UUID) a partir del nombre legible del carro
	if reporte.BusText != "" {
		busID, err := ResolverBusID(db, reporte.BusText)
		if err != nil {
			log.Printf("Advertencia: No se pudo asociar el bus a un UUID válido: %v. Se dejará la fila sin bus_id si se permite, o fallará.\n", err)
		} else {
			reporte.BusID = busID
		}
	}

	// Si no se pudo resolver el bus_id y es requerido, usaremos un UUID nulo o vacío
	if reporte.BusID == "" {
		log.Println("Advertencia: Insertando registro con bus_id vacío.")
	}

	// Validar si el Message-ID ya existe en la base de datos para evitar duplicados del mismo correo
	if reporte.MessageID != "" {
		var idDuplicado string
		queryCheckMsg := "SELECT id FROM registros_bateria WHERE correo_message_id = ? LIMIT 1"
		errCheckMsg := db.QueryRow(queryCheckMsg, reporte.MessageID).Scan(&idDuplicado)
		if errCheckMsg == nil {
			log.Printf("Base de Datos: El reporte para el correo con Message-ID %s ya existe (ID: %s). Omitiendo duplicado.\n", reporte.MessageID, idDuplicado)
			return nil
		}
	}

	// Verificar si ya existe un registro para el mismo bus, batería y día
	var idExistente, origenExistente string
	queryCheck := `
		SELECT id, origen FROM registros_bateria 
		WHERE bus_id = ? AND bateria = ? AND DATE(fecha_registro) = DATE(?)
		LIMIT 1
	`
	errCheck := db.QueryRow(queryCheck, reporte.BusID, reporte.Bateria, reporte.FechaRegistro).Scan(&idExistente, &origenExistente)

	if errCheck == nil {
		// Ya existe un registro para este día
		log.Printf("Base de Datos: Se encontró un registro existente con ID %s (origen: %s) para el bus %s (%s) en esta fecha.\n", idExistente, origenExistente, reporte.BusText, reporte.Bateria)
		
		// El script automático tiene prioridad. Actualizamos el registro existente.
		queryUpdate := `
			UPDATE registros_bateria SET 
				soc = ?, 
				soh = ?, 
				tension = ?, 
				capacidad = ?, 
				capacidad_medida = ?, 
				temperatura = ?, 
				observacion = ?, 
				usuario_cedula = ?, 
				correo_message_id = ?, 
				origen = 'automatico',
				bus_ = ?
			WHERE id = ?
		`
		_, errUpdate := db.Exec(
			queryUpdate,
			reporte.SOC,
			reporte.SOH,
			reporte.Tension,
			reporte.Capacidad,
			reporte.CapacidadMedida,
			reporte.Temperatura,
			sql.NullString{String: reporte.Observacion, Valid: reporte.Observacion != ""},
			reporte.UsuarioCedula,
			sql.NullString{String: reporte.MessageID, Valid: reporte.MessageID != ""},
			sql.NullString{String: reporte.BusText, Valid: reporte.BusText != ""},
			idExistente,
		)
		if errUpdate != nil {
			return fmt.Errorf("error al actualizar el registro de batería existente en MySQL: %w", errUpdate)
		}
		log.Printf("Base de Datos: Registro de batería existente actualizado con los datos del script con éxito (ID: %s).\n", idExistente)
		return nil
	}

	// Generar UUID único para el registro de la prueba si no existe
	reporte.ID = uuid.New().String()

	// Consulta SQL de inserción según la estructura de la base de datos (incluyendo origen)
	query := `
		INSERT INTO registros_bateria (
			id, bus_id, bateria, soc, soh, tension, capacidad, capacidad_medida, temperatura, observacion, usuario_cedula, fecha_registro, bus_, correo_message_id, origen
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'automatico')
	`

	_, err := db.Exec(
		query,
		reporte.ID,
		sql.NullString{String: reporte.BusID, Valid: reporte.BusID != ""},
		reporte.Bateria,
		reporte.SOC,
		reporte.SOH,
		reporte.Tension,
		reporte.Capacidad,
		reporte.CapacidadMedida,
		reporte.Temperatura,
		sql.NullString{String: reporte.Observacion, Valid: reporte.Observacion != ""},
		reporte.UsuarioCedula,
		reporte.FechaRegistro,
		sql.NullString{String: reporte.BusText, Valid: reporte.BusText != ""},
		sql.NullString{String: reporte.MessageID, Valid: reporte.MessageID != ""},
	)

	if err != nil {
		// Verificar si es un error de duplicado (Error 1062 de MySQL)
		if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number == 1062 {
			log.Printf("Base de Datos: El reporte para el correo con Message-ID %s ya existe. Omitiendo duplicado.\n", reporte.MessageID)
			return nil
		}
		return fmt.Errorf("error al insertar el registro de batería en MySQL: %w", err)
	}

	log.Printf("Base de Datos: Registro de batería insertado exitosamente con ID %s y origen 'automatico' en MySQL.\n", reporte.ID)
	return nil
}

// ParsearTextoAReporte analiza el texto plano formateado y lo deserializa en el modelo ReporteBateria.
func ParsearTextoAReporte(texto string) ReporteBateria {
	reporte := ReporteBateria{
		// Cédula por defecto solicitada
		UsuarioCedula: "1035126774",
		// Batería por defecto
		Bateria: "BATERÍA 1",
	}

	lineas := strings.Split(texto, "\n")
	
	// Expresiones regulares para extraer datos numéricos
	reNumeros := regexp.MustCompile(`[-+]?[0-9]*\.?[0-9]+`)

	for _, linea := range lineas {
		linea = strings.TrimSpace(linea)

		if strings.Contains(linea, "SOH") {
			match := reNumeros.FindString(linea)
			if match != "" {
				if valFloat, err := strconv.ParseFloat(match, 64); err == nil {
					reporte.SOH = valFloat
				}
			}
		} else if strings.Contains(linea, "SOC (estado de carga):") {
			// Ej: "SOC (estado de carga): 100%" -> Mapeamos el SOC a la columna soc (decimal)
			match := reNumeros.FindString(linea)
			if match != "" {
				if valFloat, err := strconv.ParseFloat(match, 64); err == nil {
					reporte.SOC = valFloat
				}
			}
		} else if strings.Contains(linea, "Tensión:") {
			// Ej: "Tensión: 12.59 V"
			reporte.Tension = strings.TrimSpace(strings.TrimPrefix(linea, "Tensión:"))
		} else if strings.Contains(linea, "Capacidad nominal:") {
			// Ej: "Capacidad nominal: 720 CCA" -> Guardamos tal cual
			reporte.Capacidad = strings.TrimSpace(strings.TrimPrefix(linea, "Capacidad nominal:"))
		} else if strings.Contains(linea, "Capacidad medida:") {
			// Ej: "Capacidad medida: 732 CCA"
			reporte.CapacidadMedida = strings.TrimSpace(strings.TrimPrefix(linea, "Capacidad medida:"))
		} else if strings.Contains(linea, "Temperatura:") {
			// Ej: "Temperatura: 35 °C"
			match := reNumeros.FindString(linea)
			if match != "" {
				if val, err := strconv.ParseFloat(match, 64); err == nil {
					reporte.Temperatura = val
				}
			}
		} else if strings.Contains(linea, "Batería:") && !strings.Contains(linea, "Tipo de batería:") {
			// Ej: "Batería: BATERÍA 2" - extraído de la sección Detalles del PDF
			bat := strings.TrimSpace(strings.TrimPrefix(linea, "Batería:"))
			if bat != "" {
				reporte.Bateria = bat
			}
		} else if strings.Contains(linea, "Carro:") {
			// Ej: "Carro: BUS001"
			reporte.BusText = strings.TrimSpace(strings.TrimPrefix(linea, "Carro:"))
		} else if strings.Contains(linea, "Consejo de reparación:") {
			// Guardar el Consejo de reparación como observación
			reporte.Observacion = strings.TrimSpace(strings.TrimPrefix(linea, "Consejo de reparación:"))
		}
	}

	// Si el bus no se detectó en el TXT, buscar con fuzzy en todo el texto
	if reporte.BusText == "" || reporte.BusText == "BUS" {
		if bus := extraerNumeroBus(texto); bus != "" {
			reporte.BusText = bus
		}
	}

	return reporte
}
