package main

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
)

// LectorGmailIMAP implementa LectorCorreo para Gmail usando IMAP.
type LectorGmailIMAP struct {
	cliente *client.Client
}

// Conectar establece la conexión SSL con el servidor de Gmail IMAP y realiza la autenticación.
func (l *LectorGmailIMAP) Conectar(servidor string, puerto int, usuario string, contrasena string) error {
	direccion := fmt.Sprintf("%s:%d", servidor, puerto)
	c, err := client.DialTLS(direccion, nil)
	if err != nil {
		return fmt.Errorf("error al conectar al servidor IMAP: %w", err)
	}

	l.cliente = c

	// Autenticación en el servidor
	if err := l.cliente.Login(usuario, contrasena); err != nil {
		l.cliente.Logout()
		return fmt.Errorf("error de autenticación: %w", err)
	}

	return nil
}

// ObtenerEnviados selecciona la carpeta de enviados y obtiene los últimos N correos.
func (l *LectorGmailIMAP) ObtenerEnviados(limite int) ([]Correo, error) {
	if l.cliente == nil {
		return nil, fmt.Errorf("lector no conectado")
	}

	// Las carpetas comunes de enviados en Gmail son "[Gmail]/Sent Mail" o "[Gmail]/Enviados"
	// Intentamos seleccionar "[Gmail]/Sent Mail" primero, si falla intentamos con "Sent Mail" o "[Gmail]/Enviados"
	nombreBuzon := "[Gmail]/Sent Mail"
	estadoBuzon, err := l.cliente.Select(nombreBuzon, true)
	if err != nil {
		// Reintento con variantes si no existe
		nombreBuzonAlt := "[Gmail]/Enviados"
		estadoBuzon, err = l.cliente.Select(nombreBuzonAlt, true)
		if err != nil {
			return nil, fmt.Errorf("no se pudo abrir la carpeta de enviados: %w", err)
		}
	}

	if estadoBuzon.Messages == 0 {
		return nil, nil
	}

	// Definir el rango de mensajes a obtener (los últimos N)
	desde := uint32(1)
	if estadoBuzon.Messages > uint32(limite) {
		desde = estadoBuzon.Messages - uint32(limite) + 1
	}
	hasta := estadoBuzon.Messages

	seqset := new(imap.SeqSet)
	seqset.AddRange(desde, hasta)

	// Solicitar la sección del sobre (Envelope) y el cuerpo del mensaje
	seccionCuerpo := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, seccionCuerpo.FetchItem()}

	mensajes := make(chan *imap.Message, limite)
	done := make(chan error, 1)

	go func() {
		done <- l.cliente.Fetch(seqset, items, mensajes)
	}()

	var correos []Correo

	for msg := range mensajes {
		correo := Correo{
			ID:        msg.SeqNum,
			MessageID: msg.Envelope.MessageId,
			Asunto:    msg.Envelope.Subject,
			Fecha:     msg.Envelope.Date,
		}

		// Obtener dirección del remitente (De)
		if len(msg.Envelope.From) > 0 {
			correo.De = fmt.Sprintf("%s <%s@%s>", msg.Envelope.From[0].PersonalName, msg.Envelope.From[0].MailboxName, msg.Envelope.From[0].HostName)
		}

		// Obtener destinatarios (Para)
		for _, para := range msg.Envelope.To {
			correo.Para = append(correo.Para, fmt.Sprintf("%s <%s@%s>", para.PersonalName, para.MailboxName, para.HostName))
		}

		// Leer el cuerpo del mensaje y adjuntos
		r := msg.GetBody(seccionCuerpo)
		if r != nil {
			mr, err := mail.CreateReader(r)
			if err == nil {
				for {
					p, err := mr.NextPart()
					if err == io.EOF {
						break
					} else if err != nil {
						break
					}

					switch h := p.Header.(type) {
					case *mail.InlineHeader:
						contentType, _, _ := h.ContentType()
						if strings.HasPrefix(contentType, "text/plain") {
							b, _ := io.ReadAll(p.Body)
							correo.Cuerpo = string(b)
						}
					case *mail.AttachmentHeader:
						nombre, err := h.Filename()
						if err != nil {
							continue
						}
						// Verificamos si es un PDF (por extensión)
						if strings.HasSuffix(strings.ToLower(nombre), ".pdf") {
							b, err := io.ReadAll(p.Body)
							if err == nil {
								correo.Adjuntos = append(correo.Adjuntos, Adjunto{
									Nombre:    nombre,
									Contenido: b,
								})
							}
						}
					}
				}
			}
		}

		// Insertar al inicio para que el más nuevo aparezca primero
		correos = append([]Correo{correo}, correos...)
	}

	if err := <-done; err != nil {
		return nil, fmt.Errorf("error al obtener mensajes: %w", err)
	}

	return correos, nil
}

// Cerrar cierra de forma segura la sesión IMAP.
func (l *LectorGmailIMAP) Cerrar() error {
	if l.cliente != nil {
		return l.cliente.Logout()
	}
	return nil
}

// Monitorear escucha en tiempo real la llegada de nuevos correos en la carpeta de enviados mediante polling periódico.
func (l *LectorGmailIMAP) Monitorear(canalCorreos chan<- Correo) error {
	if l.cliente == nil {
		return fmt.Errorf("lector no conectado")
	}

	log.Println("Iniciando monitoreo...")

	// Mantener registro del último correo procesado para evitar duplicados
	var ultimoID uint32
	var ultimaFecha time.Time

	// Obtener el ID del último correo actual para inicializar y evitar reprocesarlo
	correosIniciales, err := l.ObtenerEnviados(1)
	if err == nil && len(correosIniciales) > 0 {
		ultimoID = correosIniciales[0].ID
		ultimaFecha = correosIniciales[0].Fecha
	}

	// Ticker para comprobar la carpeta cada 10 segundos
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Volver a seleccionar el buzón para refrescar el estado de los mensajes en el servidor
		var estadoBuzonActual *imap.MailboxStatus
		nombreBuzonActual := "[Gmail]/Sent Mail"
		estadoBuzonActual, err = l.cliente.Select(nombreBuzonActual, true)
		if err != nil {
			nombreBuzonAlt := "[Gmail]/Enviados"
			estadoBuzonActual, err = l.cliente.Select(nombreBuzonAlt, true)
		}

		if err == nil && estadoBuzonActual.Messages > 0 {
			// Recuperar el último correo enviado
			nuevosCorreos, err := l.ObtenerEnviados(1)
			if err == nil && len(nuevosCorreos) > 0 {
				correoNuevo := nuevosCorreos[0]
				// Verificar si realmente es un correo nuevo comparando ID y fecha
				if correoNuevo.ID != ultimoID || !correoNuevo.Fecha.Equal(ultimaFecha) {
					log.Printf("Nuevo correo detectado en Enviados (ID anterior: %d -> Nuevo: %d). Procesando...\n", ultimoID, correoNuevo.ID)
					ultimoID = correoNuevo.ID
					ultimaFecha = correoNuevo.Fecha

					// Enviar el correo al canal para su procesamiento
					canalCorreos <- correoNuevo
				}
			} else if err != nil {
				log.Printf("Error al verificar la carpeta de enviados: %v\n", err)
			}
		}
	}

	return nil
}
