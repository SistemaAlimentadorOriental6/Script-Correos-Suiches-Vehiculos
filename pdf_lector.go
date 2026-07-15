package main

import (
	"fmt"
	"regexp"
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
// y extrae únicamente la información estructurada de la prueba de batería usando expresiones regulares globales.
func filtrarYFormatearReporte(textoCompleto string) string {
	// Normalizar espacios de no ruptura (non-breaking spaces \u00a0) y saltos de línea a espacios comunes para análisis continuo
	textoNormalizado := strings.ReplaceAll(textoCompleto, "\u00a0", " ")
	textoNormalizado = strings.ReplaceAll(textoNormalizado, "\n", " ")
	// Reducir múltiples espacios a uno solo
	reEspacios := regexp.MustCompile(`\s+`)
	textoNormalizado = reEspacios.ReplaceAllString(textoNormalizado, " ")

	var resultado []string
	
	// 1. Tipo de prueba
	tipoPrueba := "Prueba batería"
	if strings.Contains(strings.ToLower(textoNormalizado), "prueba batería fuera vehículo") || strings.Contains(strings.ToLower(textoNormalizado), "prueba bateria fuera vehiculo") {
		tipoPrueba = "Prueba batería fuera vehículo"
	} else if strings.Contains(strings.ToLower(textoNormalizado), "prueba batería en vehículo") || strings.Contains(strings.ToLower(textoNormalizado), "prueba bateria en vehiculo") {
		tipoPrueba = "Prueba batería en vehículo"
	}
	resultado = append(resultado, tipoPrueba)

	// 2. Estado de la batería
	estadoBateria := ""
	if regexp.MustCompile(`(?i)Bater[íi]a en buen(?:o)? estado`).MatchString(textoNormalizado) {
		estadoBateria = "Batería en buen estado"
	} else if regexp.MustCompile(`(?i)Reemplazar bater[íi]a`).MatchString(textoNormalizado) {
		estadoBateria = "Reemplazar batería"
	} else if regexp.MustCompile(`(?i)Cargar y volver a probar`).MatchString(textoNormalizado) {
		estadoBateria = "Cargar y volver a probar"
	} else if regexp.MustCompile(`(?i)Sustituci[oó]n bater[íi]a`).MatchString(textoNormalizado) {
		estadoBateria = "Sustitución batería"
	}
	if estadoBateria != "" {
		resultado = append(resultado, estadoBateria)
	}

	// 3. SOH
	soh := ""
	reSOH := regexp.MustCompile(`(\d+)\s*%\s*SOH`)
	matchSOH := reSOH.FindStringSubmatch(textoNormalizado)
	if len(matchSOH) > 1 {
		soh = matchSOH[1] + "%"
		resultado = append(resultado, fmt.Sprintf("%s SOH", soh))
	}

	// 4. SOC
	soc := ""
	reSOC := regexp.MustCompile(`(?i)SOC\s*\(estado de carga\)\s*:\s*(\d+)\s*%`)
	matchSOC := reSOC.FindStringSubmatch(textoNormalizado)
	if len(matchSOC) > 1 {
		soc = matchSOC[1] + "%"
		resultado = append(resultado, fmt.Sprintf("SOC (estado de carga): %s", soc))
	}

	// 5. Tipo de batería
	tipoBateria := ""
	reTipoBat := regexp.MustCompile(`(?i)Tipo de bater[íi]a\s*:\s*([A-Z0-9a-z_-]+)`)
	matchTipoBat := reTipoBat.FindStringSubmatch(textoNormalizado)
	if len(matchTipoBat) > 1 {
		tipoBateria = matchTipoBat[1]
		resultado = append(resultado, fmt.Sprintf("Tipo de batería: %s", tipoBateria))
	}

	// 6. Tensión
	tension := ""
	reTension := regexp.MustCompile(`(?i)Tensi[oó]n\s*:\s*([\d\.]+\s*V)`)
	matchTension := reTension.FindStringSubmatch(textoNormalizado)
	if len(matchTension) > 1 {
		tension = matchTension[1]
		resultado = append(resultado, fmt.Sprintf("Tensión: %s", tension))
	}

	// 7. Capacidad nominal
	capNominal := ""
	reCapNom := regexp.MustCompile(`(?i)Capacidad nominal\s*:\s*([^\s:]+\s*(?:CCA|EN|SAE|IEC|DIN))`)
	matchCapNom := reCapNom.FindStringSubmatch(textoNormalizado)
	if len(matchCapNom) > 1 {
		capNominal = matchCapNom[1]
		resultado = append(resultado, fmt.Sprintf("Capacidad nominal: %s", capNominal))
	}

	// 8. Capacidad medida
	capMedida := ""
	reCapMed := regexp.MustCompile(`(?i)Capacidad medida\s*:\s*([^\s:]+\s*(?:CCA|EN|SAE|IEC|DIN))`)
	matchCapMed := reCapMed.FindStringSubmatch(textoNormalizado)
	if len(matchCapMed) > 1 {
		capMedida = matchCapMed[1]
		resultado = append(resultado, fmt.Sprintf("Capacidad medida: %s", capMedida))
	}

	// 9. Temperatura
	temperatura := ""
	reTemp := regexp.MustCompile(`(?i)Temperatura\s*:\s*([-+]?\d+\s*°C|[-+]?\d+\s*ºC|[-+]?\d+)`)
	matchTemp := reTemp.FindStringSubmatch(textoNormalizado)
	if len(matchTemp) > 1 {
		temperatura = matchTemp[1]
		resultado = append(resultado, fmt.Sprintf("Temperatura: %s", temperatura))
	}

	// 10. Consejo de reparación
	consejo := ""
	reConsejo := regexp.MustCompile(`(?i)Consejo de reparaci[oó]n\s*:\s*(.*?)\s*(?:Inspecci[oó]n visual|Detalles:|Nombre del cliente|T[eé]cnico:|Fecha:|Nota:|$)`)
	matchConsejo := reConsejo.FindStringSubmatch(textoNormalizado)
	if len(matchConsejo) > 1 {
		consejo = strings.TrimSpace(matchConsejo[1])
		resultado = append(resultado, fmt.Sprintf("Consejo de reparación: %s", consejo))
	}

	// 11. Extraer el número de carro (BUS) con detección tolerante a errores tipográficos
	carro := extraerNumeroBus(textoNormalizado)
	resultado = append(resultado, fmt.Sprintf("Carro: %s", carro))

	return strings.Join(resultado, "\n")
}
