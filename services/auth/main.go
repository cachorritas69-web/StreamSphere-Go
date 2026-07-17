package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"streamsphere/internal/common"
)

const serviceName = "auth-user-service"

type application struct {
	db         *sql.DB
	jwtSecret  string
	serviceKey string
	tokenTTL   time.Duration
}

type user struct {
	ID        string `json:"userId"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
}

type channel struct {
	ID          string `json:"channelId"`
	OwnerID     string `json:"ownerId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"createdAt"`
}

func main() {
	port := common.Env("PORT", "8081")
	db, err := common.OpenSQLite(common.Env("DB_PATH", "/data/auth/auth.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	app := &application{
		db: db, jwtSecret: common.Env("JWT_SECRET", "change-me-in-production"),
		serviceKey: common.Env("SERVICE_KEY", "streamsphere-internal"),
		tokenTTL:   common.EnvDuration("TOKEN_TTL", 12*time.Hour),
	}
	if err := app.migrate(); err != nil {
		log.Fatal(err)
	}
	if err := app.seed(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	metrics := common.NewMetrics(serviceName)
	mux.HandleFunc("GET /health", common.HealthHandler(serviceName))
	mux.HandleFunc("GET /metrics", metrics.Handler)
	mux.HandleFunc("POST /api/auth/register", app.register)
	mux.HandleFunc("POST /api/auth/login", app.login)
	mux.Handle("GET /api/users/me", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.me)))
	mux.Handle("POST /api/channels", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.createChannel)))
	mux.Handle("GET /api/channels/me", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.myChannel)))
	mux.HandleFunc("GET /api/channels/{id}", app.getPublicChannel)
	mux.Handle("GET /internal/channels/{id}", common.RequireServiceKey(app.serviceKey)(http.HandlerFunc(app.getInternalChannel)))
	mux.Handle("GET /internal/users/{id}", common.RequireServiceKey(app.serviceKey)(http.HandlerFunc(app.getInternalUser)))

	handler := common.Chain(mux,
		common.CORS(common.Env("ALLOWED_ORIGIN", "*")),
		common.RequestID,
		common.Recover(serviceName),
		metrics.Middleware,
		common.Logging(serviceName),
	)
	server := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("%s listening on :%s", serviceName, port)
	log.Fatal(server.ListenAndServe())
}

func (a *application) migrate() error {
	return common.Migrate(a.db,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE COLLATE NOCASE,
			email TEXT NOT NULL UNIQUE COLLATE NOCASE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'VIEWER',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS channels (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			FOREIGN KEY(owner_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
	)
}

func (a *application) seed() error {
	seeds := []struct {
		username, email, password, role string
	}{
		{"admin", "admin@streamsphere.local", "Admin123!", "ADMIN"},
		{"demo", "demo@streamsphere.local", "Demo123!", "CREATOR"},
	}
	for _, seed := range seeds {
		var count int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ?`, seed.email).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(seed.password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		id := uuid.NewString()
		if _, err := a.db.Exec(`INSERT INTO users(id, username, email, password_hash, role, created_at) VALUES(?,?,?,?,?,?)`,
			id, seed.username, seed.email, string(hash), seed.role, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
		if seed.role == "CREATOR" {
			_, err = a.db.Exec(`INSERT INTO channels(id, owner_id, name, description, created_at) VALUES(?,?,?,?,?)`,
				uuid.NewString(), id, "Canal Demo", "Canal de ejemplo para probar StreamSphere", time.Now().UTC().Format(time.RFC3339))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *application) register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if len(input.Username) < 3 || len(input.Username) > 40 {
		common.Fail(w, http.StatusBadRequest, "INVALID_USERNAME", "El nombre de usuario debe tener entre 3 y 40 caracteres")
		return
	}
	parsedEmail, err := mail.ParseAddress(input.Email)
	if err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_EMAIL", "El correo electrónico no es válido")
		return
	}
	input.Email = strings.ToLower(parsedEmail.Address)
	if len(input.Password) < 8 {
		common.Fail(w, http.StatusBadRequest, "WEAK_PASSWORD", "La contraseña debe tener al menos 8 caracteres")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "HASH_ERROR", "No se pudo proteger la contraseña")
		return
	}
	createdAt := time.Now().UTC().Format(time.RFC3339)
	newUser := user{ID: uuid.NewString(), Username: input.Username, Email: input.Email, Role: "VIEWER", CreatedAt: createdAt}
	_, err = a.db.Exec(`INSERT INTO users(id, username, email, password_hash, role, created_at) VALUES(?,?,?,?,?,?)`,
		newUser.ID, newUser.Username, newUser.Email, string(hash), newUser.Role, newUser.CreatedAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			common.Fail(w, http.StatusConflict, "USER_EXISTS", "El usuario o correo ya está registrado")
			return
		}
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo registrar al usuario")
		return
	}
	token, _ := common.GenerateToken(a.jwtSecret, newUser.ID, newUser.Username, newUser.Role, a.tokenTTL)
	common.Created(w, "Usuario registrado correctamente", map[string]interface{}{
		"user": newUser, "accessToken": token, "expiresIn": int(a.tokenTTL.Seconds()),
	})
}

func (a *application) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		EmailOrUsername string `json:"emailOrUsername"`
		Password        string `json:"password"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	var found user
	var hash string
	err := a.db.QueryRow(`SELECT id, username, email, role, created_at, password_hash FROM users WHERE email = ? OR username = ?`,
		strings.TrimSpace(input.EmailOrUsername), strings.TrimSpace(input.EmailOrUsername)).Scan(
		&found.ID, &found.Username, &found.Email, &found.Role, &found.CreatedAt, &hash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		common.Fail(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Usuario o contraseña incorrectos")
		return
	}
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo iniciar sesión")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) != nil {
		common.Fail(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Usuario o contraseña incorrectos")
		return
	}
	token, err := common.GenerateToken(a.jwtSecret, found.ID, found.Username, found.Role, a.tokenTTL)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "TOKEN_ERROR", "No se pudo generar el token")
		return
	}
	common.OK(w, "Sesión iniciada", map[string]interface{}{
		"accessToken": token, "expiresIn": int(a.tokenTTL.Seconds()), "user": found,
	})
}

func (a *application) me(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	found, err := a.findUser(claims.Subject)
	if err != nil {
		common.Fail(w, http.StatusNotFound, "USER_NOT_FOUND", "Usuario no encontrado")
		return
	}
	var ownChannel *channel
	if ch, err := a.findChannelByOwner(found.ID); err == nil {
		ownChannel = &ch
	}
	common.OK(w, "Perfil autenticado", map[string]interface{}{"user": found, "channel": ownChannel})
}

func (a *application) createChannel(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Name) < 3 || len(input.Name) > 80 {
		common.Fail(w, http.StatusBadRequest, "INVALID_CHANNEL_NAME", "El nombre del canal debe tener entre 3 y 80 caracteres")
		return
	}
	if existing, err := a.findChannelByOwner(claims.Subject); err == nil {
		common.Fail(w, http.StatusConflict, "CHANNEL_EXISTS", fmt.Sprintf("Ya tienes el canal %s", existing.Name))
		return
	}
	created := channel{ID: uuid.NewString(), OwnerID: claims.Subject, Name: input.Name, Description: strings.TrimSpace(input.Description), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	tx, err := a.db.Begin()
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo crear el canal")
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO channels(id, owner_id, name, description, created_at) VALUES(?,?,?,?,?)`, created.ID, created.OwnerID, created.Name, created.Description, created.CreatedAt); err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo crear el canal")
		return
	}
	if _, err = tx.Exec(`UPDATE users SET role = 'CREATOR' WHERE id = ? AND role = 'VIEWER'`, claims.Subject); err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo actualizar el rol")
		return
	}
	if err = tx.Commit(); err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo confirmar el canal")
		return
	}
	newRole := claims.Role
	if newRole == "VIEWER" {
		newRole = "CREATOR"
	}
	newToken, _ := common.GenerateToken(a.jwtSecret, claims.Subject, claims.Username, newRole, a.tokenTTL)
	common.Created(w, "Canal creado correctamente", map[string]interface{}{"channel": created, "accessToken": newToken})
}

func (a *application) myChannel(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	found, err := a.findChannelByOwner(claims.Subject)
	if errors.Is(err, sql.ErrNoRows) {
		common.Fail(w, http.StatusNotFound, "CHANNEL_NOT_FOUND", "Aún no tienes un canal")
		return
	}
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo consultar el canal")
		return
	}
	common.OK(w, "Canal del usuario", found)
}

func (a *application) getPublicChannel(w http.ResponseWriter, r *http.Request) {
	found, err := a.findChannel(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		common.Fail(w, http.StatusNotFound, "CHANNEL_NOT_FOUND", "Canal no encontrado")
		return
	}
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo consultar el canal")
		return
	}
	common.OK(w, "Canal encontrado", found)
}

func (a *application) getInternalChannel(w http.ResponseWriter, r *http.Request) {
	a.getPublicChannel(w, r)
}

func (a *application) getInternalUser(w http.ResponseWriter, r *http.Request) {
	found, err := a.findUser(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		common.Fail(w, http.StatusNotFound, "USER_NOT_FOUND", "Usuario no encontrado")
		return
	}
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo consultar el usuario")
		return
	}
	common.OK(w, "Usuario encontrado", found)
}

func (a *application) findUser(id string) (user, error) {
	var found user
	err := a.db.QueryRow(`SELECT id, username, email, role, created_at FROM users WHERE id = ?`, id).Scan(
		&found.ID, &found.Username, &found.Email, &found.Role, &found.CreatedAt,
	)
	return found, err
}

func (a *application) findChannel(id string) (channel, error) {
	var found channel
	err := a.db.QueryRow(`SELECT id, owner_id, name, description, created_at FROM channels WHERE id = ?`, id).Scan(
		&found.ID, &found.OwnerID, &found.Name, &found.Description, &found.CreatedAt,
	)
	return found, err
}

func (a *application) findChannelByOwner(ownerID string) (channel, error) {
	var found channel
	err := a.db.QueryRow(`SELECT id, owner_id, name, description, created_at FROM channels WHERE owner_id = ?`, ownerID).Scan(
		&found.ID, &found.OwnerID, &found.Name, &found.Description, &found.CreatedAt,
	)
	return found, err
}
