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

	// Seleccionar el buzón inicialmente para obtener el número actual de mensajes
	nombreBuzon := "[Gmail]/Sent Mail"
	estadoBuzon, err := l.cliente.Select(nombreBuzon, true)
	if err != nil {
		nombreBuzonAlt := "[Gmail]/Enviados"
		estadoBuzon, err = l.cliente.Select(nombreBuzonAlt, true)
		if err != nil {
			return fmt.Errorf("error al inicializar monitoreo en carpeta de enviados: %w", err)
		}
	}

	// Mantener el registro de la cantidad de mensajes iniciales como punto de partida
	ultimoProcesadoID := estadoBuzon.Messages
	log.Printf("Monitoreo inicializado. Mensajes actuales en Enviados: %d\n", ultimoProcesadoID)

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

		if err != nil {
			log.Printf("Error al seleccionar buzón durante el monitoreo: %v\n", err)
			continue
		}

		mensajesTotales := estadoBuzonActual.Messages

		// Si hay nuevos mensajes en la carpeta
		if mensajesTotales > ultimoProcesadoID {
			diferencia := int(mensajesTotales - ultimoProcesadoID)
			log.Printf("Se detectaron %d nuevos mensajes en Enviados. Obteniendo...\n", diferencia)

			// Obtener todos los correos nuevos correspondientes a la diferencia
			nuevosCorreos, err := l.ObtenerEnviados(diferencia)
			if err != nil {
				log.Printf("Error al obtener nuevos correos: %v\n", err)
				continue
			}

			// NOTA: ObtenerEnviados inserta al inicio los más nuevos (orden descendente).
			// Para procesarlos uno a uno en orden cronológico correcto (del más antiguo al más nuevo),
			// debemos iterar sobre nuevosCorreos en orden inverso (desde el final hasta el principio).
			for i := len(nuevosCorreos) - 1; i >= 0; i-- {
				correoNuevo := nuevosCorreos[i]
				log.Printf("Procesando y encolando correo: ID %d, Asunto: %s\n", correoNuevo.ID, correoNuevo.Asunto)
				canalCorreos <- correoNuevo
			}

			// Actualizar el puntero del último procesado al total de mensajes actual
			ultimoProcesadoID = mensajesTotales
		} else if mensajesTotales < ultimoProcesadoID {
			// Si la cantidad de mensajes disminuyó (por ejemplo, porque el usuario borró correos viejos en la bandeja),
			// simplemente sincronizamos el contador sin alertar error
			ultimoProcesadoID = mensajesTotales
		}
	}

	return nil
}
