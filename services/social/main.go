package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"streamsphere/internal/common"
)

const serviceName = "social-interaction-service"

type application struct {
	db              *sql.DB
	jwtSecret       string
	serviceKey      string
	catalogURL      string
	notificationURL string
	bus             common.EventBus
}

type videoInfo struct {
	ID      string `json:"videoId"`
	OwnerID string `json:"ownerId"`
	Title   string `json:"title"`
	Status  string `json:"status"`
}

type comment struct {
	ID        string `json:"commentId"`
	VideoID   string `json:"videoId"`
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	ParentID  string `json:"parentId,omitempty"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

func main() {
	port := common.Env("PORT", "8085")
	db, err := common.OpenSQLite(common.Env("DB_PATH", "/data/social/social.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	app := &application{
		db:              db,
		jwtSecret:       common.Env("JWT_SECRET", "change-me-in-production"),
		serviceKey:      common.Env("SERVICE_KEY", "streamsphere-internal"),
		catalogURL:      common.EnvURL("CATALOG_SERVICE_URL", "http://localhost:8082"),
		notificationURL: common.EnvURL("NOTIFICATION_SERVICE_URL", "http://localhost:8087"),
		bus:             common.EventBus{URL: common.Env("RABBITMQ_URL", "")},
	}
	if err := app.migrate(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	metrics := common.NewMetrics(serviceName)
	mux.HandleFunc("GET /health", common.HealthHandler(serviceName))
	mux.HandleFunc("GET /metrics", metrics.Handler)
	mux.Handle("POST /api/videos/{id}/comments", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.createComment)))
	mux.HandleFunc("GET /api/videos/{id}/comments", app.listComments)
	mux.Handle("DELETE /api/comments/{id}", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.deleteComment)))
	mux.Handle("POST /api/videos/{id}/reactions", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.react)))
	mux.HandleFunc("GET /api/videos/{id}/reactions", app.reactionSummary)
	mux.Handle("POST /api/channels/{id}/subscriptions", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.subscribe)))
	mux.Handle("DELETE /api/channels/{id}/subscriptions", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.unsubscribe)))
	mux.Handle("GET /api/channels/{id}/subscriptions/status", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.subscriptionStatus)))
	mux.Handle("POST /api/playlists", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.createPlaylist)))
	mux.Handle("GET /api/playlists/me", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.myPlaylists)))
	mux.Handle("POST /api/playlists/{id}/items", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.addPlaylistItem)))

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
		`CREATE TABLE IF NOT EXISTS comments (
			id TEXT PRIMARY KEY, video_id TEXT NOT NULL, user_id TEXT NOT NULL, username TEXT NOT NULL,
			parent_id TEXT NOT NULL DEFAULT '', content TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'VISIBLE', created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_comments_video ON comments(video_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS reactions (
			id TEXT PRIMARY KEY, video_id TEXT NOT NULL, user_id TEXT NOT NULL, type TEXT NOT NULL, created_at TEXT NOT NULL,
			UNIQUE(video_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id TEXT PRIMARY KEY, channel_id TEXT NOT NULL, user_id TEXT NOT NULL, created_at TEXT NOT NULL,
			UNIQUE(channel_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS playlists (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL, name TEXT NOT NULL, visibility TEXT NOT NULL DEFAULT 'PRIVATE', created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS playlist_items (
			id TEXT PRIMARY KEY, playlist_id TEXT NOT NULL, video_id TEXT NOT NULL, position INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL,
			UNIQUE(playlist_id, video_id), FOREIGN KEY(playlist_id) REFERENCES playlists(id) ON DELETE CASCADE
		)`,
	)
}

func (a *application) createComment(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	videoID := r.PathValue("id")
	video, err := a.getVideo(r.Context(), videoID)
	if err != nil || video.Status != "PUBLISHED" {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "El video no está disponible")
		return
	}
	var input struct {
		Content  string `json:"content"`
		ParentID string `json:"parentId"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	input.Content = strings.TrimSpace(input.Content)
	if len(input.Content) < 1 || len(input.Content) > 1000 {
		common.Fail(w, http.StatusBadRequest, "INVALID_COMMENT", "El comentario debe tener entre 1 y 1000 caracteres")
		return
	}
	created := comment{
		ID: uuid.NewString(), VideoID: videoID, UserID: claims.Subject, Username: claims.Username,
		ParentID: strings.TrimSpace(input.ParentID), Content: input.Content, Status: "VISIBLE",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	_, err = a.db.Exec(`INSERT INTO comments(id, video_id, user_id, username, parent_id, content, status, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		created.ID, created.VideoID, created.UserID, created.Username, created.ParentID, created.Content, created.Status, created.CreatedAt)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo publicar el comentario")
		return
	}
	event := common.NewEvent("comment.created", serviceName, map[string]interface{}{
		"commentId": created.ID, "videoId": videoID, "userId": claims.Subject, "username": claims.Username,
		"ownerId": video.OwnerID, "recipientId": video.OwnerID, "videoTitle": video.Title, "content": created.Content,
	})
	a.bus.PublishBestEffort(event)
	if video.OwnerID != claims.Subject {
		go a.notifyBestEffort(event, claims.Username+" comentó tu video", created.Content, "/?video="+videoID)
	}
	common.Created(w, "Comentario publicado", created)
}

func (a *application) listComments(w http.ResponseWriter, r *http.Request) {
	page := positiveInt(r.URL.Query().Get("page"), 1)
	size := positiveInt(r.URL.Query().Get("size"), 20)
	if size > 100 {
		size = 100
	}
	videoID := r.PathValue("id")
	var total int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE video_id=? AND status='VISIBLE'`, videoID).Scan(&total); err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron contar los comentarios")
		return
	}
	rows, err := a.db.Query(`SELECT id, video_id, user_id, username, parent_id, content, status, created_at
		FROM comments WHERE video_id=? AND status='VISIBLE' ORDER BY created_at DESC LIMIT ? OFFSET ?`, videoID, size, (page-1)*size)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron consultar los comentarios")
		return
	}
	defer rows.Close()
	items := make([]comment, 0)
	for rows.Next() {
		var item comment
		if err := rows.Scan(&item.ID, &item.VideoID, &item.UserID, &item.Username, &item.ParentID, &item.Content, &item.Status, &item.CreatedAt); err != nil {
			common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron leer los comentarios")
			return
		}
		items = append(items, item)
	}
	common.OK(w, "Comentarios del video", map[string]interface{}{
		"items": items, "page": page, "size": size, "totalItems": total,
	})
}

func (a *application) deleteComment(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	var ownerID string
	err := a.db.QueryRow(`SELECT user_id FROM comments WHERE id=?`, r.PathValue("id")).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		common.Fail(w, http.StatusNotFound, "COMMENT_NOT_FOUND", "Comentario no encontrado")
		return
	}
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo consultar el comentario")
		return
	}
	if ownerID != claims.Subject && !common.HasRole(claims, "ADMIN", "MODERATOR") {
		common.Fail(w, http.StatusForbidden, "NOT_COMMENT_OWNER", "No puedes eliminar este comentario")
		return
	}
	_, err = a.db.Exec(`UPDATE comments SET status='DELETED' WHERE id=?`, r.PathValue("id"))
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo eliminar el comentario")
		return
	}
	common.OK(w, "Comentario eliminado", map[string]string{"commentId": r.PathValue("id")})
}

func (a *application) react(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	videoID := r.PathValue("id")
	video, err := a.getVideo(r.Context(), videoID)
	if err != nil {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "Video no encontrado")
		return
	}
	var input struct {
		Type string `json:"type"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	reactionType := strings.ToUpper(strings.TrimSpace(input.Type))
	if reactionType != "LIKE" && reactionType != "DISLIKE" {
		common.Fail(w, http.StatusBadRequest, "INVALID_REACTION", "La reacción debe ser LIKE o DISLIKE")
		return
	}
	id := uuid.NewString()
	_, err = a.db.Exec(`INSERT INTO reactions(id, video_id, user_id, type, created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(video_id, user_id) DO UPDATE SET type=excluded.type, created_at=excluded.created_at`,
		id, videoID, claims.Subject, reactionType, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo guardar la reacción")
		return
	}
	event := common.NewEvent("reaction.created", serviceName, map[string]interface{}{
		"videoId": videoID, "userId": claims.Subject, "ownerId": video.OwnerID, "title": video.Title, "type": reactionType,
	})
	a.bus.PublishBestEffort(event)
	common.OK(w, "Reacción guardada", map[string]string{"videoId": videoID, "type": reactionType})
}

func (a *application) reactionSummary(w http.ResponseWriter, r *http.Request) {
	var likes, dislikes int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM reactions WHERE video_id=? AND type='LIKE'`, r.PathValue("id")).Scan(&likes)
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM reactions WHERE video_id=? AND type='DISLIKE'`, r.PathValue("id")).Scan(&dislikes)
	common.OK(w, "Resumen de reacciones", map[string]int{"likes": likes, "dislikes": dislikes})
}

func (a *application) subscribe(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	channelID := r.PathValue("id")
	_, err := a.db.Exec(`INSERT INTO subscriptions(id, channel_id, user_id, created_at) VALUES(?,?,?,?)
		ON CONFLICT(channel_id, user_id) DO NOTHING`, uuid.NewString(), channelID, claims.Subject, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo registrar la suscripción")
		return
	}
	event := common.NewEvent("subscription.created", serviceName, map[string]interface{}{"channelId": channelID, "userId": claims.Subject})
	a.bus.PublishBestEffort(event)
	common.Created(w, "Suscripción registrada", map[string]interface{}{"channelId": channelID, "subscribed": true})
}

func (a *application) unsubscribe(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	_, err := a.db.Exec(`DELETE FROM subscriptions WHERE channel_id=? AND user_id=?`, r.PathValue("id"), claims.Subject)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo cancelar la suscripción")
		return
	}
	common.OK(w, "Suscripción cancelada", map[string]interface{}{"channelId": r.PathValue("id"), "subscribed": false})
}

func (a *application) subscriptionStatus(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	var count int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM subscriptions WHERE channel_id=? AND user_id=?`, r.PathValue("id"), claims.Subject).Scan(&count)
	common.OK(w, "Estado de suscripción", map[string]interface{}{"channelId": r.PathValue("id"), "subscribed": count > 0})
}

func (a *application) createPlaylist(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	var input struct {
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Name) < 2 || len(input.Name) > 80 {
		common.Fail(w, http.StatusBadRequest, "INVALID_PLAYLIST_NAME", "El nombre debe tener entre 2 y 80 caracteres")
		return
	}
	visibility := strings.ToUpper(strings.TrimSpace(input.Visibility))
	if visibility == "" {
		visibility = "PRIVATE"
	}
	if visibility != "PRIVATE" && visibility != "PUBLIC" {
		common.Fail(w, http.StatusBadRequest, "INVALID_VISIBILITY", "La playlist debe ser PRIVATE o PUBLIC")
		return
	}
	id := uuid.NewString()
	createdAt := time.Now().UTC().Format(time.RFC3339)
	_, err := a.db.Exec(`INSERT INTO playlists(id, user_id, name, visibility, created_at) VALUES(?,?,?,?,?)`, id, claims.Subject, input.Name, visibility, createdAt)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo crear la playlist")
		return
	}
	common.Created(w, "Playlist creada", map[string]interface{}{"playlistId": id, "name": input.Name, "visibility": visibility, "createdAt": createdAt})
}

func (a *application) myPlaylists(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	rows, err := a.db.Query(`SELECT p.id, p.name, p.visibility, p.created_at, COUNT(i.id)
		FROM playlists p LEFT JOIN playlist_items i ON i.playlist_id=p.id WHERE p.user_id=? GROUP BY p.id ORDER BY p.created_at DESC`, claims.Subject)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron consultar las playlists")
		return
	}
	defer rows.Close()
	items := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, name, visibility, createdAt string
		var count int
		if err := rows.Scan(&id, &name, &visibility, &createdAt, &count); err != nil {
			common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron leer las playlists")
			return
		}
		items = append(items, map[string]interface{}{"playlistId": id, "name": name, "visibility": visibility, "itemCount": count, "createdAt": createdAt})
	}
	common.OK(w, "Playlists del usuario", map[string]interface{}{"items": items})
}

func (a *application) addPlaylistItem(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	playlistID := r.PathValue("id")
	var ownerID string
	if err := a.db.QueryRow(`SELECT user_id FROM playlists WHERE id=?`, playlistID).Scan(&ownerID); errors.Is(err, sql.ErrNoRows) {
		common.Fail(w, http.StatusNotFound, "PLAYLIST_NOT_FOUND", "Playlist no encontrada")
		return
	} else if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo consultar la playlist")
		return
	}
	if ownerID != claims.Subject {
		common.Fail(w, http.StatusForbidden, "NOT_PLAYLIST_OWNER", "No puedes modificar esta playlist")
		return
	}
	var input struct {
		VideoID string `json:"videoId"`
	}
	if err := common.DecodeJSON(r, &input); err != nil || strings.TrimSpace(input.VideoID) == "" {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", "Debes indicar videoId")
		return
	}
	var position int
	_ = a.db.QueryRow(`SELECT COALESCE(MAX(position), -1)+1 FROM playlist_items WHERE playlist_id=?`, playlistID).Scan(&position)
	_, err := a.db.Exec(`INSERT INTO playlist_items(id, playlist_id, video_id, position, created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(playlist_id, video_id) DO NOTHING`, uuid.NewString(), playlistID, input.VideoID, position, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo agregar el video")
		return
	}
	common.Created(w, "Video agregado a la playlist", map[string]interface{}{"playlistId": playlistID, "videoId": input.VideoID, "position": position})
}

func (a *application) getVideo(ctx context.Context, id string) (videoInfo, error) {
	var result videoInfo
	err := common.InternalJSON(ctx, http.MethodGet, a.catalogURL+"/internal/videos/"+id, a.serviceKey, nil, &result)
	return result, err
}

func (a *application) notifyBestEffort(event common.Event, title, message, link string) {
	payload := map[string]interface{}{
		"userId": event.Data["recipientId"], "type": "NEW_COMMENT", "title": title,
		"message": message, "link": link, "sourceEventId": event.EventID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := common.InternalJSON(ctx, http.MethodPost, a.notificationURL+"/internal/notifications", a.serviceKey, payload, nil); err != nil {
		log.Printf("notification fallback failed: %v", err)
	}
}

func positiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

var _ = json.Valid
var _ = fmt.Sprintf
