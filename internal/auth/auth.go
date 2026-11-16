// Package auth valida las credenciales de la interfaz, administra las sesiones
// y limita los intentos fallidos de inicio de sesión.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// Valores por defecto del inicio de sesión.
const (
	DefaultSessionTTL  = 8 * time.Hour   // duración de una sesión desde que se inicia
	DefaultMaxFailures = 5               // intentos fallidos seguidos antes de bloquear
	DefaultBlockTime   = 1 * time.Minute // tiempo de bloqueo después de los intentos fallidos
)

var (
	ErrInvalidCredentials = errors.New("usuario o contraseña incorrectos")
	ErrTooManyAttempts    = errors.New("demasiados intentos fallidos")
)

// Session es una sesión iniciada.
type Session struct {
	ID        string
	User      string
	ExpiresAt time.Time
}

// Authenticator valida usuario y contraseña y guarda las sesiones en memoria.
// Al reiniciar la aplicación, las sesiones se pierden y hay que volver a iniciar sesión.
type Authenticator struct {
	user         string
	userHash     [32]byte
	passwordHash [32]byte
	ttl          time.Duration
	maxFailures  int
	blockTime    time.Duration
	now          func() time.Time

	mu       sync.Mutex
	sessions map[string]Session
	failures map[string]*attempts // por cliente (IP)
}

type attempts struct {
	count        int
	blockedUntil time.Time
}

// New crea un Authenticator para user y password. Devuelve nil si alguno está vacío:
// en ese caso la interfaz no pide inicio de sesión.
func New(user, password string) *Authenticator {
	if user == "" || password == "" {
		return nil
	}
	return &Authenticator{
		user:         user,
		userHash:     sha256.Sum256([]byte(user)),
		passwordHash: sha256.Sum256([]byte(password)),
		ttl:          DefaultSessionTTL,
		maxFailures:  DefaultMaxFailures,
		blockTime:    DefaultBlockTime,
		now:          time.Now,
		sessions:     make(map[string]Session),
		failures:     make(map[string]*attempts),
	}
}

// SessionTTL devuelve la duración de las sesiones.
func (a *Authenticator) SessionTTL() time.Duration {
	return a.ttl
}

// Check compara user y password con los configurados, en tiempo constante.
// Se comparan los resúmenes SHA-256 para no revelar por el tiempo de respuesta
// cuántos caracteres coinciden ni la longitud de los valores correctos.
func (a *Authenticator) Check(user, password string) bool {
	userHash := sha256.Sum256([]byte(user))
	passwordHash := sha256.Sum256([]byte(password))
	userOK := subtle.ConstantTimeCompare(userHash[:], a.userHash[:]) == 1
	passwordOK := subtle.ConstantTimeCompare(passwordHash[:], a.passwordHash[:]) == 1
	return userOK && passwordOK
}

// Login valida las credenciales del cliente client (su IP) y crea una sesión.
// Después de varios intentos fallidos seguidos, el cliente queda bloqueado un tiempo:
// devuelve ErrTooManyAttempts y cuánto falta para que pueda volver a intentar.
func (a *Authenticator) Login(user, password, client string) (Session, time.Duration, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.now()
	if f, ok := a.failures[client]; ok && now.Before(f.blockedUntil) {
		return Session{}, f.blockedUntil.Sub(now), ErrTooManyAttempts
	}

	if !a.Check(user, password) {
		f, ok := a.failures[client]
		if !ok {
			f = &attempts{}
			a.failures[client] = f
		}
		f.count++
		if f.count >= a.maxFailures {
			f.count = 0
			f.blockedUntil = now.Add(a.blockTime)
		}
		return Session{}, 0, ErrInvalidCredentials
	}

	delete(a.failures, client)
	a.removeExpired(now)

	s := Session{ID: newSessionID(), User: a.user, ExpiresAt: now.Add(a.ttl)}
	a.sessions[s.ID] = s
	return s, 0, nil
}

// Session devuelve la sesión id si existe y no ha vencido.
func (a *Authenticator) Session(id string) (Session, bool) {
	if id == "" {
		return Session{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	s, ok := a.sessions[id]
	if !ok {
		return Session{}, false
	}
	if !a.now().Before(s.ExpiresAt) {
		delete(a.sessions, id)
		return Session{}, false
	}
	return s, true
}

// Logout cierra la sesión id.
func (a *Authenticator) Logout(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, id)
}

// removeExpired borra las sesiones vencidas. Debe llamarse con a.mu tomado.
func (a *Authenticator) removeExpired(now time.Time) {
	for id, s := range a.sessions {
		if !now.Before(s.ExpiresAt) {
			delete(a.sessions, id)
		}
	}
}

// newSessionID genera un identificador aleatorio de 32 bytes, imposible de adivinar.
func newSessionID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
