package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"streamsphere/internal/common"
)

const serviceName = "notification-service"

type application struct {
	db         *sql.DB
	jwtSecret  string
	serviceKey string
	bus        common.EventBus
}

type notification struct {
	ID            string `json:"notificationId"`
	UserID        string `json:"userId"`
	Type          string `json:"type"`
	Title         string `json:"title"`
	Message       string `json:"message"`
	Link          string `json:"link"`
	Read          bool   `json:"read"`
	SourceEventID string `json:"sourceEventId,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

func main() {
	port := common.Env("PORT", "8087")
	db, err := common.OpenSQLite(common.Env("DB_PATH", "/data/notification/notification.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	app := &application{
		db:         db,
		jwtSecret:  common.Env("JWT_SECRET", "change-me-in-production"),
		serviceKey: common.Env("SERVICE_KEY", "streamsphere-internal"),
		bus:        common.EventBus{URL: common.Env("RABBITMQ_URL", "")},
	}
	if err := common.Migrate(db,
		`CREATE TABLE IF NOT EXISTS notifications (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL, type TEXT NOT NULL, title TEXT NOT NULL,
			message TEXT NOT NULL, link TEXT NOT NULL DEFAULT '', read INTEGER NOT NULL DEFAULT 0,
			source_event_id TEXT UNIQUE, created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(user_id, read, created_at)`,
	); err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.bus.StartConsumer(ctx, "streamsphere.notifications", []string{"video.processed", "video.failed", "comment.created"}, app.processEvent)

	mux := http.NewServeMux()
	metrics := common.NewMetrics(serviceName)
	mux.HandleFunc("GET /health", common.HealthHandler(serviceName))
	mux.HandleFunc("GET /metrics", metrics.Handler)
	mux.Handle("POST /internal/notifications", common.RequireServiceKey(app.serviceKey)(http.HandlerFunc(app.createInternal)))
	mux.Handle("GET /api/notifications/me", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.listMine)))
	mux.Handle("PATCH /api/notifications/{id}/read", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.markRead)))
	mux.Handle("PATCH /api/notifications/read-all", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.markAllRead)))

	handler := common.Chain(mux,
		common.CORS(common.Env("ALLOWED_ORIGIN", "*")), common.RequestID,
		common.Recover(serviceName), metrics.Middleware, common.Logging(serviceName),
	)
	server := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("%s listening on :%s", serviceName, port)
	log.Fatal(server.ListenAndServe())
}

func (a *application) createInternal(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID        string `json:"userId"`
		Type          string `json:"type"`
		Title         string `json:"title"`
		Message       string `json:"message"`
		Link          string `json:"link"`
		SourceEventID string `json:"sourceEventId"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	created, err := a.insert(input.UserID, input.Type, input.Title, input.Message, input.Link, input.SourceEventID)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo crear la notificación")
		return
	}
	common.Created(w, "Notificación creada", created)
}

func (a *application) processEvent(event common.Event) error {
	userID := stringValue(event.Data["recipientId"])
	if userID == "" {
		userID = stringValue(event.Data["ownerId"])
	}
	if userID == "" {
		return nil
	}
	var notificationType, title, message, link string
	switch event.EventType {
	case "video.processed":
		notificationType = "VIDEO_READY"
		title = "Tu video ya está disponible"
		message = stringValue(event.Data["title"]) + " terminó de procesarse."
		link = "/?video=" + stringValue(event.Data["videoId"])
	case "video.failed":
		notificationType = "VIDEO_FAILED"
		title = "Falló el procesamiento del video"
		message = stringValue(event.Data["error"])
	case "comment.created":
		if stringValue(event.Data["userId"]) == userID {
			return nil
		}
		notificationType = "NEW_COMMENT"
		title = stringValue(event.Data["username"]) + " comentó tu video"
		message = stringValue(event.Data["content"])
		link = "/?video=" + stringValue(event.Data["videoId"])
	default:
		return nil
	}
	_, err := a.insert(userID, notificationType, title, message, link, event.EventID)
	return err
}

func (a *application) insert(userID, notificationType, title, message, link, sourceEventID string) (notification, error) {
	created := notification{
		ID: uuid.NewString(), UserID: strings.TrimSpace(userID), Type: strings.TrimSpace(notificationType),
		Title: strings.TrimSpace(title), Message: strings.TrimSpace(message), Link: strings.TrimSpace(link),
		Read: false, SourceEventID: strings.TrimSpace(sourceEventID), CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if created.UserID == "" || created.Title == "" {
		return created, errors.New("userId y title son obligatorios")
	}
	var source interface{} = created.SourceEventID
	if created.SourceEventID == "" {
		source = nil
	}
	_, err := a.db.Exec(`INSERT INTO notifications(id, user_id, type, title, message, link, read, source_event_id, created_at)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(source_event_id) DO NOTHING`,
		created.ID, created.UserID, created.Type, created.Title, created.Message, created.Link, 0, source, created.CreatedAt)
	return created, err
}

func (a *application) listMine(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	page := positiveInt(r.URL.Query().Get("page"), 1)
	size := positiveInt(r.URL.Query().Get("size"), 30)
	if size > 100 {
		size = 100
	}
	var unread int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id=? AND read=0`, claims.Subject).Scan(&unread)
	rows, err := a.db.Query(`SELECT id, user_id, type, title, message, link, read, COALESCE(source_event_id,''), created_at
		FROM notifications WHERE user_id=? ORDER BY created_at DESC LIMIT ? OFFSET ?`, claims.Subject, size, (page-1)*size)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron consultar las notificaciones")
		return
	}
	defer rows.Close()
	items := make([]notification, 0)
	for rows.Next() {
		var item notification
		var read int
		if err := rows.Scan(&item.ID, &item.UserID, &item.Type, &item.Title, &item.Message, &item.Link, &read, &item.SourceEventID, &item.CreatedAt); err != nil {
			common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron leer las notificaciones")
			return
		}
		item.Read = read == 1
		items = append(items, item)
	}
	common.OK(w, "Notificaciones del usuario", map[string]interface{}{"items": items, "unreadCount": unread, "page": page})
}

func (a *application) markRead(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	result, err := a.db.Exec(`UPDATE notifications SET read=1 WHERE id=? AND user_id=?`, r.PathValue("id"), claims.Subject)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo actualizar la notificación")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		common.Fail(w, http.StatusNotFound, "NOTIFICATION_NOT_FOUND", "Notificación no encontrada")
		return
	}
	common.OK(w, "Notificación marcada como leída", map[string]interface{}{"notificationId": r.PathValue("id"), "read": true})
}

func (a *application) markAllRead(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	result, err := a.db.Exec(`UPDATE notifications SET read=1 WHERE user_id=? AND read=0`, claims.Subject)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron actualizar las notificaciones")
		return
	}
	count, _ := result.RowsAffected()
	common.OK(w, "Notificaciones marcadas como leídas", map[string]interface{}{"updated": count})
}

func positiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}
