package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
)

// extraerTextoPDF lee un archivo PDF desde el disco y extrae todo su contenido de texto fila por fila para evitar problemas de caracteres divididos.
func extraerTextoPDF(ruta string) (string, error) {
	f, r, err := pdf.Open(ruta)
	if err != nil {
		return "", fmt.Errorf("error al abrir el PDF: %w", err)
	}
	defer f.Close()

	var buf strings.Builder
	totalPage := r.NumPage()
	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}
		rows, err := p.GetTextByRow()
		if err != nil {
			return "", fmt.Errorf("error al obtener texto por fila en la página %d: %w", pageIndex, err)
		}
		for _, row := range rows {
			var line strings.Builder
			for _, w := range row.Content {
				line.WriteString(w.S)
			}
			buf.WriteString(line.String())
			buf.WriteString("\n")
		}
	}

	return buf.String(), nil
}

// filtrarYFormatearReporte analiza el texto completo del reporte de Autel MaxiBAS
// y extrae únicamente la información estructurada de la prueba de batería.
func filtrarYFormatearReporte(textoCompleto string) string {
	// Normalizar espacios de no ruptura (non-breaking spaces \u00a0) a espacios comunes
	textoNormalizado := strings.ReplaceAll(textoCompleto, "\u00a0", " ")
	
	lineasRaw := strings.Split(textoNormalizado, "\n")
	var lineas []string
	
	// Limpiar espacios en blanco de cada línea
	for _, l := range lineasRaw {
		lineas = append(lineas, strings.TrimSpace(l))
	}

	var resultado []string
	
	// Variables para almacenar los datos extraídos
	var tipoPrueba string
	var estadoBateria string
	var soh string
	var soc string
	var tipoBateria string
	var tension string
	var capNominal string
	var capMedida string
	var temperatura string
	var consejo string

	// Buscar datos clave
	for i := 0; i < len(lineas); i++ {
		linea := lineas[i]

		if strings.Contains(linea, "Prueba batería fuera vehículo") || strings.Contains(linea, "Prueba batería en vehículo") {
			tipoPrueba = linea
		}

		if strings.Contains(linea, "Batería en bueno estado") || strings.Contains(linea, "Batería en buen estado") {
			estadoBateria = "Batería en buen estado"
		} else if strings.Contains(linea, "Reemplazar batería") {
			estadoBateria = "Reemplazar batería"
		} else if strings.Contains(linea, "Cargar y volver a probar") {
			estadoBateria = "Cargar y volver a probar"
		} else if strings.Contains(linea, "Sustitución batería") {
			estadoBateria = "Sustitución batería"
		}

		if linea == "SOH" && i > 0 {
			// El porcentaje de SOH suele estar en la línea anterior
			soh = lineas[i-1]
		}

		// SOC y Temperatura pueden agruparse en la misma fila al leer por coordenadas
		if strings.Contains(linea, "SOC (estado de carga):") && i+1 < len(lineas) {
			siguiente := lineas[i+1]
			if strings.Contains(linea, "Temperatura:") && strings.Contains(siguiente, "%") && strings.Contains(siguiente, "°C") {
				reValores := regexp.MustCompile(`(\d+%\s*)(\d+\s*°C)`)
				matches := reValores.FindStringSubmatch(siguiente)
				if len(matches) > 2 {
					soc = strings.TrimSpace(matches[1])
					temperatura = strings.TrimSpace(matches[2])
				} else {
					soc = siguiente
				}
			} else {
				soc = siguiente
			}
		}

		if strings.Contains(linea, "Tipo de batería:") && i+1 < len(lineas) {
			tipoBateria = lineas[i+1]
		}

		if strings.Contains(linea, "Tensión:") && i+1 < len(lineas) {
			tension = lineas[i+1]
		}

		if strings.Contains(linea, "Capacidad nominal:") && i+1 < len(lineas) {
			capNominal = lineas[i+1]
		}

		if strings.Contains(linea, "Capacidad medida:") && i+1 < len(lineas) {
			capMedida = lineas[i+1]
		}

		// Solo asignar si no fue previamente extraída por el análisis combinado de SOC
		if strings.Contains(linea, "Temperatura:") && !strings.Contains(linea, "SOC (estado de carga):") && i+1 < len(lineas) {
			temperatura = lineas[i+1]
		}

		if strings.Contains(linea, "Consejo de reparación:") {
			if len(linea) > len("Consejo de reparación:") {
				consejo = strings.TrimSpace(strings.TrimPrefix(linea, "Consejo de reparación:"))
			} else if i+1 < len(lineas) {
				consejo = lineas[i+1]
			}
		}
	}

	// Armar reporte organizado estructurado en español
	if tipoPrueba != "" {
		resultado = append(resultado, tipoPrueba)
	} else {
		resultado = append(resultado, "Prueba batería")
	}

	if estadoBateria != "" {
		resultado = append(resultado, estadoBateria)
	}
	if soh != "" {
		resultado = append(resultado, fmt.Sprintf("%s SOH", soh))
	}
	if soc != "" {
		resultado = append(resultado, fmt.Sprintf("SOC (estado de carga): %s", soc))
	}
	if tipoBateria != "" {
		resultado = append(resultado, fmt.Sprintf("Tipo de batería: %s", tipoBateria))
	}
	if tension != "" {
		resultado = append(resultado, fmt.Sprintf("Tensión: %s", tension))
	}
	if capNominal != "" {
		resultado = append(resultado, fmt.Sprintf("Capacidad nominal: %s", capNominal))
	}
	if capMedida != "" {
		resultado = append(resultado, fmt.Sprintf("Capacidad medida: %s", capMedida))
	}
	if temperatura != "" {
		resultado = append(resultado, fmt.Sprintf("Temperatura: %s", temperatura))
	}
	if consejo != "" {
		resultado = append(resultado, fmt.Sprintf("Consejo de reparación: %s", consejo))
	}

	// Extraer el número de carro (BUS) del Consejo de reparación
	var carro string
	if consejo != "" {
		re := regexp.MustCompile(`(?i)bus\s*(\d+)`)
		match := re.FindStringSubmatch(consejo)
		if len(match) > 1 {
			digitos := match[1]
			// Convertir a int para formatear con ceros a la izquierda (ej: 001)
			if num, err := strconv.Atoi(digitos); err == nil {
				carro = fmt.Sprintf("BUS%03d", num)
			} else {
				carro = "BUS" + digitos
			}
		}
	}
	resultado = append(resultado, fmt.Sprintf("Carro: %s", carro))

	return strings.Join(resultado, "\n")
}
