package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DatosReporte representa la información limpia y estructurada que se enviará al Webhook.
type DatosReporte struct {
	De              string    `json:"de"`
	Fecha           time.Time `json:"fecha"`
	Asunto          string    `json:"asunto"`
	TextoFormateado string    `json:"texto_formateado"`
}

// EnviarWebhook despacha un HTTP POST con el reporte formateado a la URL configurada.
func EnviarWebhook(url string, reporte DatosReporte) error {
	if url == "" {
		return nil // No hacer nada si no hay URL configurada
	}

	payload, err := json.Marshal(reporte)
	if err != nil {
		return fmt.Errorf("error al serializar reporte a JSON: %w", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("error al realizar petición POST al webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("el webhook respondió con un código de estado no exitoso: %d", resp.StatusCode)
	}

	return nil
}
