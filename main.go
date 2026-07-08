package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	// Credenciales de Gmail configuradas directamente en texto plano
	usuario := "saomejoracontinua@gmail.com"
	contrasena := "eemt juhn unzd epvd" // Asegúrate de colocar tu contraseña de aplicación de Gmail aquí

	// URL del Webhook para enviar la información estructurada de la batería.
	webhookURL := "" // Ejemplo: "https://webhook.site/tu-id-de-prueba"

	// Configuración de la base de datos MySQL
	dbUsuario := "desarrollo"
	dbContrasena := "test_24*"
	dbHost := "192.168.90.32"
	dbPuerto := "3306"
	dbNombre := "bdsaocomco_inspeccion_baterias"

	dbDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", dbUsuario, dbContrasena, dbHost, dbPuerto, dbNombre)

	if usuario == "" || contrasena == "" {
		fmt.Println("Error: Debes configurar el usuario y la contraseña de Gmail directamente en el código.")
		os.Exit(1)
	}

	// Conectar a la base de datos MySQL
	fmt.Println("Conectando a la base de datos MySQL...")
	db, err := ConectarBD(dbDSN)
	if err != nil {
		log.Printf("Advertencia: No se pudo conectar a la base de datos MySQL: %v. El script continuará sin base de datos.\n", err)
	} else {
		fmt.Println("Conexión establecida con éxito a MySQL.")
		defer db.Close()
	}

	fmt.Println("Conectando al servidor IMAP de Gmail...")

	// Aplicación del principio de Inversión de Dependencia (SOLID)
	var lector LectorCorreo = &LectorGmailIMAP{}

	err = lector.Conectar("imap.gmail.com", 993, usuario, contrasena)
	if err != nil {
		log.Fatalf("Error al conectar: %v", err)
	}
	defer lector.Cerrar()

	fmt.Println("Conexión establecida con éxito a Gmail.")

	// Crear carpeta para descargas si no existe
	directorioDescargas := "descargas"
	if err := os.MkdirAll(directorioDescargas, 0755); err != nil {
		log.Fatalf("Error al crear la carpeta de descargas: %v", err)
	}

	fmt.Println("Obteniendo el último correo enviado...")
	correos, err := lector.ObtenerEnviados(1)
	if err != nil {
		log.Fatalf("Error al obtener el último correo enviado: %v", err)
	}

	fmt.Printf("\nSe encontraron %d correos en el historial:\n", len(correos))
	fmt.Println(strings.Repeat("=", 60))
	for _, correo := range correos {
		procesarCorreo(correo, directorioDescargas, webhookURL, db)
	}

	// ==========================================
	// Modo Escucha en Tiempo Real (Tipo Webhook)
	// ==========================================
	canalCorreos := make(chan Correo, 10)

	// Iniciar la escucha en segundo plano
	go func() {
		err := lector.Monitorear(canalCorreos)
		if err != nil {
			log.Printf("Error en el monitoreo en tiempo real: %v\n", err)
		}
	}()

	fmt.Println("\nServicio en escucha activa esperando nuevos correos enviados...")
	fmt.Println("Presiona Ctrl+C para detener.")
	fmt.Println(strings.Repeat("=", 60))

	// Bucle para procesar las notificaciones reactivas de nuevos correos
	for correoNuevo := range canalCorreos {
		fmt.Printf("\n>>> NUEVO CORREO DETECTADO (%s) <<<\n", correoNuevo.Fecha.Format("15:04:05"))
		procesarCorreo(correoNuevo, directorioDescargas, webhookURL, db)
	}
}

// procesarCorreo contiene la lógica para procesar un correo, extraer adjuntos, leer el PDF, guardar en la BD y disparar el webhook.
func procesarCorreo(correo Correo, directorioDescargas string, webhookURL string, db *sql.DB) {
	// Filtrar correos que no contienen adjuntos PDF
	if len(correo.Adjuntos) == 0 {
		return
	}

	fmt.Printf("De: %s\n", correo.De)
	fmt.Printf("Para: %s\n", strings.Join(correo.Para, ", "))
	fmt.Printf("Fecha: %s\n", correo.Fecha.Format("2006-01-02 15:04:05"))
	fmt.Printf("Asunto: %s\n", correo.Asunto)
	fmt.Printf("Message-ID: %s\n", correo.MessageID)

	fmt.Println("    Archivos PDF Adjuntos:")
	for _, adjunto := range correo.Adjuntos {
		prefijoFecha := correo.Fecha.Format("20060102_150405")
		nombreUnico := fmt.Sprintf("%s_%s", prefijoFecha, adjunto.Nombre)

		rutaArchivo := filepath.Join(directorioDescargas, nombreUnico)
		err := os.WriteFile(rutaArchivo, adjunto.Contenido, 0644)
		if err != nil {
			fmt.Printf("      - [ERROR] No se pudo guardar %s: %v\n", nombreUnico, err)
			continue
		}

		fmt.Printf("      - [Descargado] %s -> Guardado en: %s\n", adjunto.Nombre, rutaArchivo)

		// Extraer y guardar texto del PDF
		fmt.Println("        [Analizando PDF] Extrayendo contenido de texto...")
		textoExtraido, errExtraccion := extraerTextoPDF(rutaArchivo)
		if errExtraccion != nil {
			fmt.Printf("        - [ERROR PDF] No se pudo leer el contenido del PDF: %v\n", errExtraccion)
			continue
		}

		// Eliminar el PDF del disco para no acumular archivos innecesarios
		if errBorrar := os.Remove(rutaArchivo); errBorrar != nil {
			fmt.Printf("        - [ADVERTENCIA] No se pudo eliminar el PDF %s: %v\n", rutaArchivo, errBorrar)
		} else {
			fmt.Printf("        - [PDF Eliminado] %s borrado del disco.\n", rutaArchivo)
		}

		// Generar nombre de archivo TXT reemplazando la extensión
		nombreTXT := strings.TrimSuffix(nombreUnico, filepath.Ext(nombreUnico)) + ".txt"
		rutaTXT := filepath.Join(directorioDescargas, nombreTXT)

		// Formatear y filtrar únicamente la información deseada de la batería
		textoFormateado := filtrarYFormatearReporte(textoExtraido)

		errTXT := os.WriteFile(rutaTXT, []byte(textoFormateado), 0644)
		if errTXT != nil {
			fmt.Printf("        - [ERROR TXT] No se pudo escribir el archivo de texto %s: %v\n", nombreTXT, errTXT)
		} else {
			fmt.Printf("        - [Guardado TXT] Reporte limpio guardado en: %s\n", rutaTXT)

			// Persistir datos en MySQL si la conexión a la base de datos está activa
			if db != nil {
				fmt.Println("        [Base de Datos] Guardando registro de batería en MySQL...")
				errDB := GuardarReporteBateria(db, textoFormateado, correo.Fecha, correo.MessageID)
				if errDB != nil {
					fmt.Printf("        - [ERROR BD] No se pudo registrar en la base de datos: %v\n", errDB)
				}
			}

			// Disparar Webhook HTTP POST con el reporte formateado en JSON
			if webhookURL != "" {
				fmt.Println("        [Enviando Webhook] Despachando reporte a la URL del Webhook...")
				reporte := DatosReporte{
					De:              correo.De,
					Fecha:           correo.Fecha,
					Asunto:          correo.Asunto,
					TextoFormateado: textoFormateado,
				}

				errWebhook := EnviarWebhook(webhookURL, reporte)
				if errWebhook != nil {
					fmt.Printf("        - [ERROR WEBHOOK] Fallo al enviar al webhook: %v\n", errWebhook)
				} else {
					fmt.Println("        - [Webhook OK] Datos del reporte enviados exitosamente.")
				}
			}
		}
	}
	fmt.Println(strings.Repeat("-", 60))
}

// indentarTexto añade un prefijo a cada línea del texto para mejorar la legibilidad en consola.
func indentarTexto(texto string, prefijo string) string {
	lineas := strings.Split(texto, "\n")
	for i, linea := range lineas {
		lineas[i] = prefijo + strings.TrimSpace(linea)
	}
	return strings.Join(lineas, "\n")
}
