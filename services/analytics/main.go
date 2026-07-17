package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"streamsphere/internal/common"
)

const serviceName = "analytics-history-service"

type application struct {
	db         *sql.DB
	jwtSecret  string
	serviceKey string
	bus        common.EventBus
}

func main() {
	port := common.Env("PORT", "8086")
	db, err := common.OpenSQLite(common.Env("DB_PATH", "/data/analytics/analytics.db"))
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
	if err := app.migrate(); err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.bus.StartConsumer(ctx, "streamsphere.analytics", []string{"playback.*", "reaction.*", "comment.*"}, app.processEvent)

	mux := http.NewServeMux()
	metrics := common.NewMetrics(serviceName)
	mux.HandleFunc("GET /health", common.HealthHandler(serviceName))
	mux.HandleFunc("GET /metrics", metrics.Handler)
	mux.Handle("POST /internal/events", common.RequireServiceKey(app.serviceKey)(http.HandlerFunc(app.ingestEvent)))
	mux.Handle("GET /api/history/me", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.history)))
	mux.Handle("GET /api/analytics/creators/me", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.creatorMetrics)))
	mux.HandleFunc("GET /api/analytics/videos/{id}", app.videoMetrics)

	handler := common.Chain(mux,
		common.CORS(common.Env("ALLOWED_ORIGIN", "*")), common.RequestID,
		common.Recover(serviceName), metrics.Middleware, common.Logging(serviceName),
	)
	server := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("%s listening on :%s", serviceName, port)
	log.Fatal(server.ListenAndServe())
}

func (a *application) migrate() error {
	return common.Migrate(a.db,
		`CREATE TABLE IF NOT EXISTS processed_events (
			event_id TEXT PRIMARY KEY, event_type TEXT NOT NULL, payload_json TEXT NOT NULL, occurred_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS watch_history (
			user_id TEXT NOT NULL, video_id TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', last_second INTEGER NOT NULL DEFAULT 0,
			completed INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL, PRIMARY KEY(user_id, video_id)
		)`,
		`CREATE TABLE IF NOT EXISTS creator_metrics (
			owner_id TEXT NOT NULL, video_id TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', views INTEGER NOT NULL DEFAULT 0,
			watch_time INTEGER NOT NULL DEFAULT 0, likes INTEGER NOT NULL DEFAULT 0, comments INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL, PRIMARY KEY(owner_id, video_id)
		)`,
	)
}

func (a *application) ingestEvent(w http.ResponseWriter, r *http.Request) {
	var event common.Event
	if err := common.DecodeJSON(r, &event); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_EVENT", err.Error())
		return
	}
	if event.EventID == "" || event.EventType == "" {
		common.Fail(w, http.StatusBadRequest, "INVALID_EVENT", "El evento requiere eventId y eventType")
		return
	}
	if err := a.processEvent(event); err != nil {
		common.Fail(w, http.StatusInternalServerError, "EVENT_ERROR", "No se pudo procesar el evento")
		return
	}
	common.OK(w, "Evento procesado", map[string]string{"eventId": event.EventID})
}

func (a *application) processEvent(event common.Event) error {
	payload, _ := json.Marshal(event)
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO processed_events(event_id, event_type, payload_json, occurred_at) VALUES(?,?,?,?) ON CONFLICT(event_id) DO NOTHING`,
		event.EventID, event.EventType, string(payload), event.OccurredAt)
	if err != nil {
		return err
	}
	inserted, _ := result.RowsAffected()
	if inserted == 0 {
		return tx.Commit()
	}
	videoID := stringValue(event.Data["videoId"])
	userID := stringValue(event.Data["userId"])
	ownerID := stringValue(event.Data["ownerId"])
	title := stringValue(event.Data["title"])
	second := intValue(event.Data["second"])
	now := time.Now().UTC().Format(time.RFC3339)

	if strings.HasPrefix(event.EventType, "playback.") {
		completed := 0
		viewsIncrement := 0
		if event.EventType == "playback.completed" {
			completed = 1
			viewsIncrement = 1
		}
		if userID != "" && videoID != "" {
			_, err = tx.Exec(`INSERT INTO watch_history(user_id, video_id, title, last_second, completed, updated_at) VALUES(?,?,?,?,?,?)
				ON CONFLICT(user_id, video_id) DO UPDATE SET title=excluded.title, last_second=MAX(watch_history.last_second, excluded.last_second),
				completed=MAX(watch_history.completed, excluded.completed), updated_at=excluded.updated_at`,
				userID, videoID, title, second, completed, now)
			if err != nil {
				return err
			}
		}
		if ownerID != "" && videoID != "" {
			_, err = tx.Exec(`INSERT INTO creator_metrics(owner_id, video_id, title, views, watch_time, updated_at) VALUES(?,?,?,?,?,?)
				ON CONFLICT(owner_id, video_id) DO UPDATE SET title=excluded.title,
				views=creator_metrics.views+excluded.views, watch_time=creator_metrics.watch_time+excluded.watch_time, updated_at=excluded.updated_at`,
				ownerID, videoID, title, viewsIncrement, second, now)
			if err != nil {
				return err
			}
		}
	}
	if event.EventType == "reaction.created" && ownerID != "" && videoID != "" && strings.EqualFold(stringValue(event.Data["type"]), "LIKE") {
		_, err = tx.Exec(`INSERT INTO creator_metrics(owner_id, video_id, title, likes, updated_at) VALUES(?,?,?,?,?)
			ON CONFLICT(owner_id, video_id) DO UPDATE SET likes=creator_metrics.likes+1, updated_at=excluded.updated_at`, ownerID, videoID, title, 1, now)
		if err != nil {
			return err
		}
	}
	if event.EventType == "comment.created" && ownerID != "" && videoID != "" {
		_, err = tx.Exec(`INSERT INTO creator_metrics(owner_id, video_id, title, comments, updated_at) VALUES(?,?,?,?,?)
			ON CONFLICT(owner_id, video_id) DO UPDATE SET comments=creator_metrics.comments+1, updated_at=excluded.updated_at`, ownerID, videoID, title, 1, now)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *application) history(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	rows, err := a.db.Query(`SELECT video_id, title, last_second, completed, updated_at FROM watch_history WHERE user_id=? ORDER BY updated_at DESC LIMIT 100`, claims.Subject)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo consultar el historial")
		return
	}
	defer rows.Close()
	items := make([]map[string]interface{}, 0)
	for rows.Next() {
		var videoID, title, updatedAt string
		var lastSecond, completed int
		if err := rows.Scan(&videoID, &title, &lastSecond, &completed, &updatedAt); err != nil {
			common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo leer el historial")
			return
		}
		items = append(items, map[string]interface{}{
			"videoId": videoID, "title": title, "lastSecond": lastSecond, "completed": completed == 1, "updatedAt": updatedAt,
		})
	}
	common.OK(w, "Historial de reproducción", map[string]interface{}{"items": items})
}

func (a *application) creatorMetrics(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	if !common.HasRole(claims, "CREATOR", "ADMIN") {
		common.Fail(w, http.StatusForbidden, "CREATOR_REQUIRED", "Se requiere rol de creador")
		return
	}
	rows, err := a.db.Query(`SELECT video_id, title, views, watch_time, likes, comments, updated_at FROM creator_metrics WHERE owner_id=?`, claims.Subject)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron consultar las métricas")
		return
	}
	defer rows.Close()
	items := make([]map[string]interface{}, 0)
	totalViews, totalWatch, totalLikes, totalComments := 0, 0, 0, 0
	for rows.Next() {
		var videoID, title, updatedAt string
		var views, watchTime, likes, comments int
		if err := rows.Scan(&videoID, &title, &views, &watchTime, &likes, &comments, &updatedAt); err != nil {
			common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron leer las métricas")
			return
		}
		totalViews += views
		totalWatch += watchTime
		totalLikes += likes
		totalComments += comments
		items = append(items, map[string]interface{}{
			"videoId": videoID, "title": title, "views": views, "watchTime": watchTime,
			"likes": likes, "comments": comments, "updatedAt": updatedAt,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["views"].(int) > items[j]["views"].(int) })
	if len(items) > 10 {
		items = items[:10]
	}
	common.OK(w, "Métricas del creador", map[string]interface{}{
		"views": totalViews, "watchTime": totalWatch, "likes": totalLikes, "comments": totalComments, "topVideos": items,
	})
}

func (a *application) videoMetrics(w http.ResponseWriter, r *http.Request) {
	var views, watchTime, likes, comments int
	_ = a.db.QueryRow(`SELECT COALESCE(SUM(views),0), COALESCE(SUM(watch_time),0), COALESCE(SUM(likes),0), COALESCE(SUM(comments),0)
		FROM creator_metrics WHERE video_id=?`, r.PathValue("id")).Scan(&views, &watchTime, &likes, &comments)
	common.OK(w, "Métricas del video", map[string]int{"views": views, "watchTime": watchTime, "likes": likes, "comments": comments})
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func intValue(value interface{}) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}
