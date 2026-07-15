package main

import (
	"regexp"
	"strconv"
	"strings"
)

// normalizarParaComparacion convierte el texto a minúsculas y elimina tildes/acentos.
func normalizarParaComparacion(s string) string {
	s = strings.ToLower(s)
	reemplazos := [][2]string{
		{"á", "a"}, {"à", "a"}, {"ä", "a"}, {"â", "a"},
		{"é", "e"}, {"è", "e"}, {"ë", "e"}, {"ê", "e"},
		{"í", "i"}, {"ì", "i"}, {"ï", "i"}, {"î", "i"},
		{"ó", "o"}, {"ò", "o"}, {"ö", "o"}, {"ô", "o"},
		{"ú", "u"}, {"ù", "u"}, {"ü", "u"}, {"û", "u"},
		{"ñ", "n"},
	}
	for _, r := range reemplazos {
		s = strings.ReplaceAll(s, r[0], r[1])
	}
	return s
}

// levenshtein calcula la distancia de edición mínima entre dos cadenas de texto.
// Permite detectar palabras con errores tipográficos.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		dp[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			if ra[i-1] == rb[j-1] {
				dp[i][j] = dp[i-1][j-1]
			} else {
				m := dp[i-1][j]
				if dp[i][j-1] < m {
					m = dp[i][j-1]
				}
				if dp[i-1][j-1] < m {
					m = dp[i-1][j-1]
				}
				dp[i][j] = 1 + m
			}
		}
	}
	return dp[la][lb]
}

var reDigitos = regexp.MustCompile(`\d+`)
var reTokens = regexp.MustCompile(`[a-z0-9]+`)

// buscarNumeroConFuzzy busca en el texto una palabra similar a `objetivo` (con tolerancia `maxDist`)
// y retorna el número que aparezca pegado o en los siguientes tokens.
// Retorna la cadena numérica encontrada o "" si no se encuentra.
func buscarNumeroConFuzzy(texto string, objetivo string, maxDist int) string {
	textoNorm := normalizarParaComparacion(texto)
	tokens := reTokens.FindAllStringIndex(textoNorm, -1)

	for i, loc := range tokens {
		token := textoNorm[loc[0]:loc[1]]

		// Solo comparar tokens de longitud cercana al objetivo (±2 caracteres)
		if len(token) < len(objetivo)-2 || len(token) > len(objetivo)+3 {
			continue
		}

		// Extraer la parte no numérica del token para comparar con el objetivo
		parteTexto := reDigitos.ReplaceAllString(token, "")
		parteNum := reDigitos.FindString(token)

		dist := levenshtein(parteTexto, objetivo)
		if dist <= maxDist {
			// Si el número está dentro del mismo token (ej: "bus01", "bateria2")
			if parteNum != "" {
				return parteNum
			}
			// Si no, buscar el número en los siguientes 3 tokens
			for j := i + 1; j < len(tokens) && j <= i+3; j++ {
				siguiente := textoNorm[tokens[j][0]:tokens[j][1]]
				if reDigitos.MatchString(siguiente) && !reTokens.MatchString(reDigitos.ReplaceAllString(siguiente, "")) {
					return siguiente
				}
				// Si el siguiente token es puramente numérico
				if regexp.MustCompile(`^\d+$`).MatchString(siguiente) {
					return siguiente
				}
			}
		}
	}
	return ""
}

// extraerNumeroBus busca de forma tolerante el número del bus en el texto.
// Acepta variaciones como: bus01, BUS 002, bús, vus, b.u.s, us001, buz, etc.
// Retorna el número formateado como "BUS001" o "" si no se encuentra.
func extraerNumeroBus(texto string) string {
	digitos := buscarNumeroConFuzzy(texto, "bus", 1)
	if digitos == "" {
		return ""
	}
	if num, err := strconv.Atoi(digitos); err == nil {
		return "BUS" + padLeft(num, 3)
	}
	return "BUS" + digitos
}

// extraerNumeroBateria busca de forma tolerante el número de batería en el texto.
// Acepta variaciones como: bateria1, BATERÍA 2, vateria, bateri, batrea, etc.
// Retorna el número formateado como "BATERÍA 1" o "" si no se encuentra.
func extraerNumeroBateria(texto string) string {
	digitos := buscarNumeroConFuzzy(texto, "bateria", 2)
	if digitos == "" {
		return ""
	}
	return "BATERÍA " + digitos
}

// padLeft formatea un entero con ceros a la izquierda hasta la longitud indicada.
func padLeft(n int, ancho int) string {
	s := strconv.Itoa(n)
	for len(s) < ancho {
		s = "0" + s
	}
	return s
}
