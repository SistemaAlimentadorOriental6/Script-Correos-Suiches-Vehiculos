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
func (l *LectorGmailIMAP) Monitorear(canalCorreos chan<- Correo) error {
	if l.cliente == nil {
		return fmt.Errorf("lector no conectado")
	}

	log.Println("Iniciando monitoreo...")

	// Seleccionar el buzón inicialmente para obtener el número actual de mensajes
	estadoBuzon, err := l.seleccionarCarpetaEnviados()
	if err != nil {
		return fmt.Errorf("error al inicializar monitoreo en carpeta de enviados: %w", err)
	}

	ultimoProcesadoID := estadoBuzon.Messages
	log.Printf("Monitoreo inicializado. Mensajes actuales en Enviados: %d\n", ultimoProcesadoID)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		estadoBuzonActual, err := l.seleccionarCarpetaEnviados()
		if err != nil {
			// Detectar si la sesión expiró y reconectar
			if strings.Contains(err.Error(), "Not logged in") || strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "connection reset") {
				log.Println("Sesión IMAP expirada. Reconectando...")
				for intento := 1; intento <= 5; intento++ {
					if errRecon := l.reconectar(); errRecon != nil {
						espera := time.Duration(intento*30) * time.Second
						log.Printf("Reconexión fallida (intento %d/5): %v. Reintentando en %s...\n", intento, errRecon, espera)
						time.Sleep(espera)
					} else {
						log.Println("Reconexión exitosa al servidor IMAP.")
						break
					}
				}
			} else {
				log.Printf("Error al seleccionar buzón durante el monitoreo: %v\n", err)
			}
			continue
		}

		mensajesTotales := estadoBuzonActual.Messages

		if mensajesTotales > ultimoProcesadoID {
			diferencia := int(mensajesTotales - ultimoProcesadoID)
			log.Printf("Se detectaron %d nuevos mensajes en Enviados. Obteniendo...\n", diferencia)

			nuevosCorreos, err := l.ObtenerEnviados(diferencia)
			if err != nil {
				log.Printf("Error al obtener nuevos correos: %v\n", err)
				continue
			}

			// NOTA: ObtenerEnviados inserta al inicio los más nuevos (orden descendente).
			// Para procesarlos en orden cronológico correcto iteramos en reversa.
			for i := len(nuevosCorreos) - 1; i >= 0; i-- {
				correoNuevo := nuevosCorreos[i]
				log.Printf("Procesando y encolando correo: ID %d, Asunto: %s\n", correoNuevo.ID, correoNuevo.Asunto)
				canalCorreos <- correoNuevo
			}

			ultimoProcesadoID = mensajesTotales
		} else if mensajesTotales < ultimoProcesadoID {
			ultimoProcesadoID = mensajesTotales
		}
	}

	return nil
}
