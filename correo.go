package main

import "time"

// Adjunto representa un archivo adjunto dentro de un correo.
type Adjunto struct {
	Nombre    string `json:"nombre"`
	Contenido []byte `json:"contenido"`
}

// Correo representa los datos básicos de un mensaje de correo electrónico.
type Correo struct {
	ID        uint32    `json:"id"`
	MessageID string    `json:"message_id"`
	De        string    `json:"de"`
	Para      []string  `json:"para"`
	Asunto    string    `json:"asunto"`
	Fecha     time.Time `json:"fecha"`
	Cuerpo    string    `json:"cuerpo"`
	Adjuntos  []Adjunto `json:"adjuntos"`
}

// ReporteBateria representa la estructura exacta de la fila en la tabla registros_bateria de MySQL.
type ReporteBateria struct {
	ID              string    `json:"id"`
	BusID           string    `json:"bus_id"`
	Bateria         string    `json:"bateria"`
	SOC             float64   `json:"soc"`
	Tension         string    `json:"tension"`
	Capacidad       string    `json:"capacidad"`
	CapacidadMedida string    `json:"capacidad_medida"`
	Temperatura     float64   `json:"temperatura"`
	UsuarioCedula   string    `json:"usuario_cedula"`
	FechaRegistro   time.Time `json:"fecha_registro"`
	BusText         string    `json:"bus_"`
	MessageID       string    `json:"correo_message_id"`
}

// LectorCorreo define el contrato para conectarse y leer correos de un servidor.
type LectorCorreo interface {
	Conectar(servidor string, puerto int, usuario string, contrasena string) error
	ObtenerEnviados(limite int) ([]Correo, error)
	Monitorear(canalCorreos chan<- Correo) error
	Cerrar() error
}
