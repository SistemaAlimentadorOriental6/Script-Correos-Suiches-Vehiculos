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

// LectorGmailIMAP implementa LectorCorreo para un servidor IMAP con reconexión automática.
type LectorGmailIMAP struct {
	cliente    *client.Client
	servidor   string
	puerto     int
	usuario    string
	contrasena string
}

// Conectar establece la conexión SSL con el servidor IMAP y realiza la autenticación.
// Guarda las credenciales para poder reconectarse automáticamente si la sesión expira.
func (l *LectorGmailIMAP) Conectar(servidor string, puerto int, usuario string, contrasena string) error {
	l.servidor = servidor
	l.puerto = puerto
	l.usuario = usuario
	l.contrasena = contrasena
	return l.reconectar()
}

// reconectar cierra la conexión actual (si existe) y abre una sesión IMAP nueva.
func (l *LectorGmailIMAP) reconectar() error {
	if l.cliente != nil {
		_ = l.cliente.Logout()
		l.cliente = nil
	}

	direccion := fmt.Sprintf("%s:%d", l.servidor, l.puerto)
	c, err := client.DialTLS(direccion, nil)
	if err != nil {
		return fmt.Errorf("error al conectar al servidor IMAP: %w", err)
	}

	l.cliente = c

	if err := l.cliente.Login(l.usuario, l.contrasena); err != nil {
		l.cliente.Logout()
		l.cliente = nil
		return fmt.Errorf("error de autenticación: %w", err)
	}

	return nil
}

// seleccionarCarpetaEnviados selecciona la bandeja de entrada (INBOX).
func (l *LectorGmailIMAP) seleccionarCarpetaEnviados() (*imap.MailboxStatus, error) {
	estadoBuzon, err := l.cliente.Select("INBOX", true)
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir la bandeja de entrada INBOX: %w", err)
	}
	return estadoBuzon, nil
}

// ObtenerEnviados selecciona la bandeja de entrada y obtiene los últimos N correos enviados por mejoracontinua@sao6.com.co.
func (l *LectorGmailIMAP) ObtenerEnviados(limite int) ([]Correo, error) {
	if l.cliente == nil {
		return nil, fmt.Errorf("lector no conectado")
	}

	_, err := l.seleccionarCarpetaEnviados()
	if err != nil {
		return nil, err
	}

	// Buscar correos que sean del remitente mejoracontinua@sao6.com.co
	criteria := imap.NewSearchCriteria()
	criteria.Header.Set("FROM", "mejoracontinua@sao6.com.co")
	ids, err := l.cliente.Search(criteria)
	if err != nil {
		return nil, fmt.Errorf("error al buscar correos en INBOX: %w", err)
	}

	if len(ids) == 0 {
		return nil, nil
	}

	// Tomar los últimos N ids correspondientes al límite
	if len(ids) > limite {
		ids = ids[len(ids)-limite:]
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(ids...)

	// Solicitar la sección del sobre (Envelope) y el cuerpo del mensaje
	seccionCuerpo := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, seccionCuerpo.FetchItem()}

	mensajes := make(chan *imap.Message, len(ids))
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
				if correo.MessageID == "" {
					if msgID := mr.Header.Get("Message-ID"); msgID != "" {
						correo.MessageID = strings.Trim(msgID, "<>")
					}
				}
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
// Si la sesión IMAP expira (error "Not logged in"), intenta reconectarse automáticamente.
// Monitorear escucha en tiempo real la llegada de nuevos correos en la carpeta de recibidos (INBOX)
// filtrando únicamente los que provengan de mejoracontinua@sao6.com.co.
// Si la sesión IMAP expira, intenta reconectarse automáticamente.
func (l *LectorGmailIMAP) Monitorear(canalCorreos chan<- Correo) error {
	if l.cliente == nil {
		return fmt.Errorf("lector no conectado")
	}

	log.Println("Iniciando monitoreo de nuevos correos de mejoracontinua@sao6.com.co en INBOX...")

	// 1. Obtener el estado inicial y los correos existentes del remitente
	_, err := l.seleccionarCarpetaEnviados()
	if err != nil {
		return fmt.Errorf("error al inicializar monitoreo: %w", err)
	}

	criteria := imap.NewSearchCriteria()
	criteria.Header.Set("FROM", "mejoracontinua@sao6.com.co")
	idsExistentes, err := l.cliente.Search(criteria)
	if err != nil {
		return fmt.Errorf("error al buscar correos iniciales: %w", err)
	}

	var maxID uint32
	for _, id := range idsExistentes {
		if id > maxID {
			maxID = id
		}
	}
	log.Printf("Monitoreo inicializado. ID de correo máximo conocido del remitente: %d\n", maxID)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		_, err := l.seleccionarCarpetaEnviados()
		if err != nil {
			// Detectar si la sesión expiró y reconectar
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "not logged in") || strings.Contains(errStr, "eof") || strings.Contains(errStr, "connection reset") || strings.Contains(errStr, "broken pipe") || strings.Contains(errStr, "closed") {
				log.Println("Sesión IMAP expirada o conexión rota. Reconectando...")
				reconectado := false
				for intento := 1; intento <= 5; intento++ {
					if errRecon := l.reconectar(); errRecon != nil {
						espera := time.Duration(intento*30) * time.Second
						log.Printf("Reconexión fallida (intento %d/5): %v. Reintentando en %s...\n", intento, errRecon, espera)
						time.Sleep(espera)
					} else {
						log.Println("Reconexión exitosa al servidor IMAP.")
						reconectado = true
						break
					}
				}
				if !reconectado {
					return fmt.Errorf("no se pudo reconectar al servidor IMAP tras 5 intentos")
				}
			} else {
				log.Printf("Error al seleccionar buzón durante el monitoreo: %v\n", err)
			}
			continue
		}

		// Buscar correos del remitente
		idsActuales, err := l.cliente.Search(criteria)
		if err != nil {
			log.Printf("Error al buscar correos durante el monitoreo: %v\n", err)
			continue
		}

		// Encontrar nuevos IDs mayores al máximo procesado
		var idsNuevos []uint32
		for _, id := range idsActuales {
			if id > maxID {
				idsNuevos = append(idsNuevos, id)
			}
		}

		if len(idsNuevos) > 0 {
			log.Printf("Se detectaron %d nuevos correos de mejoracontinua@sao6.com.co. Obteniendo...\n", len(idsNuevos))

			// Traer y procesar esos correos específicos
			seqset := new(imap.SeqSet)
			seqset.AddNum(idsNuevos...)

			seccionCuerpo := &imap.BodySectionName{}
			items := []imap.FetchItem{imap.FetchEnvelope, seccionCuerpo.FetchItem()}

			mensajes := make(chan *imap.Message, len(idsNuevos))
			done := make(chan error, 1)

			go func() {
				done <- l.cliente.Fetch(seqset, items, mensajes)
			}()

			var nuevosCorreos []Correo
			for msg := range mensajes {
				correo := Correo{
					ID:        msg.SeqNum,
					MessageID: msg.Envelope.MessageId,
					Asunto:    msg.Envelope.Subject,
					Fecha:     msg.Envelope.Date,
				}

				if len(msg.Envelope.From) > 0 {
					correo.De = fmt.Sprintf("%s <%s@%s>", msg.Envelope.From[0].PersonalName, msg.Envelope.From[0].MailboxName, msg.Envelope.From[0].HostName)
				}

				for _, para := range msg.Envelope.To {
					correo.Para = append(correo.Para, fmt.Sprintf("%s <%s@%s>", para.PersonalName, para.MailboxName, para.HostName))
				}

				r := msg.GetBody(seccionCuerpo)
				if r != nil {
					mr, err := mail.CreateReader(r)
					if err == nil {
						if correo.MessageID == "" {
							if msgID := mr.Header.Get("Message-ID"); msgID != "" {
								correo.MessageID = strings.Trim(msgID, "<>")
							}
						}
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

				nuevosCorreos = append([]Correo{correo}, nuevosCorreos...)
			}

			if errFetch := <-done; errFetch != nil {
				log.Printf("Error al traer nuevos correos: %v\n", errFetch)
				continue
			}

			// Encolar y actualizar el ID máximo conocido
			for i := len(nuevosCorreos) - 1; i >= 0; i-- {
				correoNuevo := nuevosCorreos[i]
				log.Printf("Procesando y encolando correo: ID %d, Asunto: %s\n", correoNuevo.ID, correoNuevo.Asunto)
				canalCorreos <- correoNuevo
			}

			for _, id := range idsNuevos {
				if id > maxID {
					maxID = id
				}
			}
		}
	}

	return nil
}
