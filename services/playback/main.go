package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"streamsphere/internal/common"
)

const serviceName = "playback-service"

type application struct {
	db           *sql.DB
	jwtSecret    string
	serviceKey   string
	catalogURL   string
	analyticsURL string
	publicBase   string
	bus          common.EventBus
}

type videoInfo struct {
	ID           string `json:"videoId"`
	OwnerID      string `json:"ownerId"`
	Title        string `json:"title"`
	Visibility   string `json:"visibility"`
	Status       string `json:"status"`
	PlaybackURL  string `json:"playbackUrl"`
	ThumbnailURL string `json:"thumbnailUrl"`
}

func main() {
	port := common.Env("PORT", "8084")
	db, err := common.OpenSQLite(common.Env("DB_PATH", "/data/playback/playback.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	app := &application{
		db:           db,
		jwtSecret:    common.Env("JWT_SECRET", "change-me-in-production"),
		serviceKey:   common.Env("SERVICE_KEY", "streamsphere-internal"),
		catalogURL:   common.EnvURL("CATALOG_SERVICE_URL", "http://localhost:8082"),
		analyticsURL: common.EnvURL("ANALYTICS_SERVICE_URL", "http://localhost:8086"),
		publicBase:   common.EnvURL("PUBLIC_BASE_URL", "http://localhost:8080"),
		bus:          common.EventBus{URL: common.Env("RABBITMQ_URL", "")},
	}
	if err := common.Migrate(db,
		`CREATE TABLE IF NOT EXISTS playback_events (
			id TEXT PRIMARY KEY,
			video_id TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL,
			second INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_playback_video ON playback_events(video_id, created_at)`,
	); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	metrics := common.NewMetrics(serviceName)
	mux.HandleFunc("GET /health", common.HealthHandler(serviceName))
	mux.HandleFunc("GET /metrics", metrics.Handler)
	mux.Handle("GET /api/playback/videos/{id}/manifest", common.Authenticate(app.jwtSecret, false)(http.HandlerFunc(app.manifest)))
	mux.Handle("POST /api/playback/events", common.Authenticate(app.jwtSecret, false)(http.HandlerFunc(app.recordEvent)))

	handler := common.Chain(mux,
		common.CORS(common.Env("ALLOWED_ORIGIN", "*")), common.RequestID,
		common.Recover(serviceName), metrics.Middleware, common.Logging(serviceName),
	)
	server := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("%s listening on :%s", serviceName, port)
	log.Fatal(server.ListenAndServe())
}

func (a *application) manifest(w http.ResponseWriter, r *http.Request) {
	video, err := a.getVideo(r.Context(), r.PathValue("id"))
	if err != nil {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "Video no encontrado")
		return
	}
	claims, authenticated := common.ClaimsFromRequest(r)
	allowed := video.Status == "PUBLISHED" && (video.Visibility == "PUBLIC" || video.Visibility == "UNLISTED")
	if authenticated && (claims.Subject == video.OwnerID || common.HasRole(claims, "ADMIN", "MODERATOR")) {
		allowed = video.Status == "PUBLISHED" || video.Status == "PROCESSING" || video.Status == "DRAFT"
	}
	if !allowed || video.PlaybackURL == "" {
		common.Fail(w, http.StatusConflict, "VIDEO_NOT_READY", "El video todavía no está disponible para reproducción")
		return
	}
	streamURL := video.PlaybackURL
	if strings.HasPrefix(streamURL, "/") {
		streamURL = a.publicBase + streamURL
	}
	common.OK(w, "URL de reproducción generada", map[string]interface{}{
		"videoId":     video.ID,
		"manifestUrl": streamURL,
		"streamUrl":   streamURL,
		"expiresAt":   time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
	})
}

func (a *application) recordEvent(w http.ResponseWriter, r *http.Request) {
	var input struct {
		VideoID   string `json:"videoId"`
		EventType string `json:"eventType"`
		Second    int    `json:"second"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	input.EventType = strings.ToUpper(strings.TrimSpace(input.EventType))
	allowed := map[string]bool{"STARTED": true, "PROGRESS": true, "COMPLETED": true}
	if input.VideoID == "" || !allowed[input.EventType] || input.Second < 0 {
		common.Fail(w, http.StatusBadRequest, "INVALID_EVENT", "Evento de reproducción inválido")
		return
	}
	video, err := a.getVideo(r.Context(), input.VideoID)
	if err != nil || video.Status != "PUBLISHED" {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "Video no disponible")
		return
	}
	userID := ""
	if claims, ok := common.ClaimsFromRequest(r); ok {
		userID = claims.Subject
	}
	eventID := uuid.NewString()
	createdAt := time.Now().UTC().Format(time.RFC3339)
	_, err = a.db.Exec(`INSERT INTO playback_events(id, video_id, user_id, event_type, second, created_at) VALUES(?,?,?,?,?,?)`,
		eventID, input.VideoID, userID, input.EventType, input.Second, createdAt)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo registrar el evento")
		return
	}
	routingKey := "playback." + strings.ToLower(input.EventType)
	event := common.Event{
		EventID:    eventID,
		EventType:  routingKey,
		OccurredAt: createdAt,
		Producer:   serviceName,
		Data: map[string]interface{}{
			"videoId":   input.VideoID,
			"userId":    userID,
			"ownerId":   video.OwnerID,
			"title":     video.Title,
			"eventType": input.EventType,
			"second":    input.Second,
		},
	}
	a.bus.PublishBestEffort(event)
	go a.analyticsFallback(event)
	common.Created(w, "Evento de reproducción registrado", map[string]string{"eventId": eventID})
}

func (a *application) getVideo(ctx context.Context, id string) (videoInfo, error) {
	var result videoInfo
	err := common.InternalJSON(ctx, http.MethodGet, a.catalogURL+"/internal/videos/"+id, a.serviceKey, nil, &result)
	return result, err
}

func (a *application) analyticsFallback(event common.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := common.InternalJSON(ctx, http.MethodPost, a.analyticsURL+"/internal/events", a.serviceKey, event, nil); err != nil {
		log.Printf("analytics fallback failed event=%s error=%v", event.EventID, err)
	}
}

var _ = errors.Is
