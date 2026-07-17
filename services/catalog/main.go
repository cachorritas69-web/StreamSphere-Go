package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"streamsphere/internal/common"
)

const serviceName = "video-catalog-service"

type application struct {
	db         *sql.DB
	jwtSecret  string
	serviceKey string
	authURL    string
}

type video struct {
	ID           string   `json:"videoId"`
	ChannelID    string   `json:"channelId"`
	OwnerID      string   `json:"ownerId"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Category     string   `json:"category"`
	Tags         []string `json:"tags"`
	Visibility   string   `json:"visibility"`
	Status       string   `json:"status"`
	ThumbnailURL string   `json:"thumbnailUrl"`
	PlaybackURL  string   `json:"playbackUrl"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
}

type channelInfo struct {
	ID      string `json:"channelId"`
	OwnerID string `json:"ownerId"`
	Name    string `json:"name"`
}

func main() {
	port := common.Env("PORT", "8082")
	db, err := common.OpenSQLite(common.Env("DB_PATH", "/data/catalog/catalog.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	app := &application{
		db: db, jwtSecret: common.Env("JWT_SECRET", "change-me-in-production"),
		serviceKey: common.Env("SERVICE_KEY", "streamsphere-internal"),
		authURL:    common.EnvURL("AUTH_SERVICE_URL", "http://localhost:8081"),
	}
	if err := app.migrate(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	metrics := common.NewMetrics(serviceName)
	mux.HandleFunc("GET /health", common.HealthHandler(serviceName))
	mux.HandleFunc("GET /metrics", metrics.Handler)
	mux.Handle("POST /api/videos", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.createVideo)))
	mux.HandleFunc("GET /api/videos/search", app.searchVideos)
	mux.Handle("GET /api/videos/mine", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.myVideos)))
	mux.Handle("GET /api/videos/{id}", common.Authenticate(app.jwtSecret, false)(http.HandlerFunc(app.getVideo)))
	mux.Handle("PATCH /api/videos/{id}/visibility", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.changeVisibility)))
	mux.Handle("PATCH /api/videos/{id}", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.updateVideo)))
	mux.Handle("PATCH /api/admin/videos/{id}/status", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.moderateVideo)))
	mux.Handle("GET /internal/videos/{id}", common.RequireServiceKey(app.serviceKey)(http.HandlerFunc(app.getInternalVideo)))
	mux.Handle("PATCH /internal/videos/{id}/processing", common.RequireServiceKey(app.serviceKey)(http.HandlerFunc(app.updateProcessing)))

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
		`CREATE TABLE IF NOT EXISTS videos (
			id TEXT PRIMARY KEY,
			channel_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT 'General',
			tags_json TEXT NOT NULL DEFAULT '[]',
			visibility TEXT NOT NULL DEFAULT 'PUBLIC',
			status TEXT NOT NULL DEFAULT 'DRAFT',
			thumbnail_url TEXT NOT NULL DEFAULT '',
			playback_url TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_videos_public ON videos(status, visibility, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_videos_owner ON videos(owner_id, created_at)`,
	)
}

func (a *application) createVideo(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	if !common.HasRole(claims, "CREATOR", "ADMIN") {
		common.Fail(w, http.StatusForbidden, "CREATOR_REQUIRED", "Necesitas crear un canal antes de publicar videos")
		return
	}
	var input struct {
		ChannelID   string   `json:"channelId"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Category    string   `json:"category"`
		Tags        []string `json:"tags"`
		Visibility  string   `json:"visibility"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.ChannelID = strings.TrimSpace(input.ChannelID)
	if len(input.Title) < 3 || len(input.Title) > 150 {
		common.Fail(w, http.StatusBadRequest, "INVALID_TITLE", "El título debe tener entre 3 y 150 caracteres")
		return
	}
	if input.ChannelID == "" {
		common.Fail(w, http.StatusBadRequest, "CHANNEL_REQUIRED", "Debes indicar el canal")
		return
	}
	channel, err := a.getChannel(r.Context(), input.ChannelID)
	if err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_CHANNEL", "El canal indicado no existe")
		return
	}
	if channel.OwnerID != claims.Subject && claims.Role != "ADMIN" {
		common.Fail(w, http.StatusForbidden, "NOT_CHANNEL_OWNER", "No puedes publicar en este canal")
		return
	}
	input.Visibility = strings.ToUpper(strings.TrimSpace(input.Visibility))
	if input.Visibility == "" {
		input.Visibility = "PUBLIC"
	}
	if input.Visibility != "PUBLIC" && input.Visibility != "PRIVATE" && input.Visibility != "UNLISTED" {
		common.Fail(w, http.StatusBadRequest, "INVALID_VISIBILITY", "La visibilidad debe ser PUBLIC, PRIVATE o UNLISTED")
		return
	}
	if strings.TrimSpace(input.Category) == "" {
		input.Category = "General"
	}
	cleanTags := make([]string, 0, len(input.Tags))
	for _, tag := range input.Tags {
		tag = strings.TrimSpace(strings.ToLower(tag))
		if tag != "" && len(cleanTags) < 10 {
			cleanTags = append(cleanTags, tag)
		}
	}
	tagsJSON, _ := json.Marshal(cleanTags)
	now := time.Now().UTC().Format(time.RFC3339)
	created := video{
		ID: uuid.NewString(), ChannelID: input.ChannelID, OwnerID: claims.Subject,
		Title: input.Title, Description: strings.TrimSpace(input.Description),
		Category: strings.TrimSpace(input.Category), Tags: cleanTags, Visibility: input.Visibility,
		Status: "DRAFT", CreatedAt: now, UpdatedAt: now,
	}
	_, err = a.db.Exec(`INSERT INTO videos(id, channel_id, owner_id, title, description, category, tags_json, visibility, status, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, created.ID, created.ChannelID, created.OwnerID, created.Title, created.Description,
		created.Category, string(tagsJSON), created.Visibility, created.Status, created.CreatedAt, created.UpdatedAt)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo crear el video")
		return
	}
	common.Created(w, "Video creado. Ahora carga el archivo multimedia.", created)
}

func (a *application) searchVideos(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	page := positiveInt(r.URL.Query().Get("page"), 1)
	size := positiveInt(r.URL.Query().Get("size"), 12)
	if size > 50 {
		size = 50
	}
	conditions := []string{"status = 'PUBLISHED'", "visibility = 'PUBLIC'"}
	args := make([]interface{}, 0)
	if query != "" {
		conditions = append(conditions, `(title LIKE ? OR description LIKE ? OR tags_json LIKE ?)`)
		like := "%" + query + "%"
		args = append(args, like, like, like)
	}
	if category != "" && !strings.EqualFold(category, "Todas") {
		conditions = append(conditions, `category = ? COLLATE NOCASE`)
		args = append(args, category)
	}
	where := strings.Join(conditions, " AND ")
	var total int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM videos WHERE `+where, args...).Scan(&total); err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo buscar videos")
		return
	}
	args = append(args, size, (page-1)*size)
	rows, err := a.db.Query(`SELECT id, channel_id, owner_id, title, description, category, tags_json, visibility, status,
		thumbnail_url, playback_url, created_at, updated_at FROM videos WHERE `+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo buscar videos")
		return
	}
	defer rows.Close()
	items, err := scanVideos(rows)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron leer los resultados")
		return
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + size - 1) / size
	}
	common.OK(w, "Resultados de búsqueda", map[string]interface{}{
		"items": items, "page": page, "size": size, "totalItems": total, "totalPages": totalPages,
	})
}

func (a *application) myVideos(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	rows, err := a.db.Query(`SELECT id, channel_id, owner_id, title, description, category, tags_json, visibility, status,
		thumbnail_url, playback_url, created_at, updated_at FROM videos WHERE owner_id = ? ORDER BY created_at DESC`, claims.Subject)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron consultar tus videos")
		return
	}
	defer rows.Close()
	items, err := scanVideos(rows)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudieron leer tus videos")
		return
	}
	common.OK(w, "Videos del creador", map[string]interface{}{"items": items})
}

func (a *application) getVideo(w http.ResponseWriter, r *http.Request) {
	found, err := a.findVideo(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "Video no encontrado")
		return
	}
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo consultar el video")
		return
	}
	claims, authenticated := common.ClaimsFromRequest(r)
	public := found.Status == "PUBLISHED" && (found.Visibility == "PUBLIC" || found.Visibility == "UNLISTED")
	owner := authenticated && (claims.Subject == found.OwnerID || claims.Role == "ADMIN" || claims.Role == "MODERATOR")
	if !public && !owner {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "Video no encontrado o no disponible")
		return
	}
	common.OK(w, "Detalle del video", found)
}

func (a *application) getInternalVideo(w http.ResponseWriter, r *http.Request) {
	found, err := a.findVideo(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "Video no encontrado")
		return
	}
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo consultar el video")
		return
	}
	common.OK(w, "Detalle interno del video", found)
}

func (a *application) updateVideo(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	found, err := a.findVideo(r.PathValue("id"))
	if err != nil {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "Video no encontrado")
		return
	}
	if found.OwnerID != claims.Subject && claims.Role != "ADMIN" {
		common.Fail(w, http.StatusForbidden, "NOT_VIDEO_OWNER", "No puedes editar este video")
		return
	}
	var input struct {
		Title       *string   `json:"title"`
		Description *string   `json:"description"`
		Category    *string   `json:"category"`
		Tags        *[]string `json:"tags"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if input.Title != nil {
		found.Title = strings.TrimSpace(*input.Title)
	}
	if input.Description != nil {
		found.Description = strings.TrimSpace(*input.Description)
	}
	if input.Category != nil {
		found.Category = strings.TrimSpace(*input.Category)
	}
	if input.Tags != nil {
		found.Tags = *input.Tags
	}
	if len(found.Title) < 3 || len(found.Title) > 150 {
		common.Fail(w, http.StatusBadRequest, "INVALID_TITLE", "El título debe tener entre 3 y 150 caracteres")
		return
	}
	tagsJSON, _ := json.Marshal(found.Tags)
	found.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	_, err = a.db.Exec(`UPDATE videos SET title=?, description=?, category=?, tags_json=?, updated_at=? WHERE id=?`,
		found.Title, found.Description, found.Category, string(tagsJSON), found.UpdatedAt, found.ID)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo actualizar el video")
		return
	}
	common.OK(w, "Video actualizado", found)
}

func (a *application) changeVisibility(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	found, err := a.findVideo(r.PathValue("id"))
	if err != nil {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "Video no encontrado")
		return
	}
	if found.OwnerID != claims.Subject && claims.Role != "ADMIN" {
		common.Fail(w, http.StatusForbidden, "NOT_VIDEO_OWNER", "No puedes cambiar este video")
		return
	}
	var input struct {
		Visibility string `json:"visibility"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	visibility := strings.ToUpper(strings.TrimSpace(input.Visibility))
	if visibility != "PUBLIC" && visibility != "PRIVATE" && visibility != "UNLISTED" {
		common.Fail(w, http.StatusBadRequest, "INVALID_VISIBILITY", "Visibilidad inválida")
		return
	}
	_, err = a.db.Exec(`UPDATE videos SET visibility=?, updated_at=? WHERE id=?`, visibility, time.Now().UTC().Format(time.RFC3339), found.ID)
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo cambiar la visibilidad")
		return
	}
	found.Visibility = visibility
	common.OK(w, "Visibilidad actualizada", found)
}

func (a *application) moderateVideo(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	if !common.HasRole(claims, "ADMIN", "MODERATOR") {
		common.Fail(w, http.StatusForbidden, "MODERATOR_REQUIRED", "Se requiere rol de moderación")
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	status := strings.ToUpper(strings.TrimSpace(input.Status))
	if status != "BLOCKED" && status != "PUBLISHED" {
		common.Fail(w, http.StatusBadRequest, "INVALID_STATUS", "El estado debe ser BLOCKED o PUBLISHED")
		return
	}
	result, err := a.db.Exec(`UPDATE videos SET status=?, updated_at=? WHERE id=?`, status, time.Now().UTC().Format(time.RFC3339), r.PathValue("id"))
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo moderar el video")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "Video no encontrado")
		return
	}
	common.OK(w, "Estado de moderación actualizado", map[string]string{"videoId": r.PathValue("id"), "status": status})
}

func (a *application) updateProcessing(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status       string `json:"status"`
		ThumbnailURL string `json:"thumbnailUrl"`
		PlaybackURL  string `json:"playbackUrl"`
	}
	if err := common.DecodeJSON(r, &input); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	status := strings.ToUpper(strings.TrimSpace(input.Status))
	allowed := map[string]bool{"PROCESSING": true, "PUBLISHED": true, "FAILED": true, "BLOCKED": true}
	if !allowed[status] {
		common.Fail(w, http.StatusBadRequest, "INVALID_STATUS", "Estado de procesamiento inválido")
		return
	}
	result, err := a.db.Exec(`UPDATE videos SET status=?, thumbnail_url=?, playback_url=?, updated_at=? WHERE id=?`,
		status, input.ThumbnailURL, input.PlaybackURL, time.Now().UTC().Format(time.RFC3339), r.PathValue("id"))
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo actualizar el procesamiento")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "Video no encontrado")
		return
	}
	common.OK(w, "Procesamiento actualizado", map[string]string{"videoId": r.PathValue("id"), "status": status})
}

func (a *application) findVideo(id string) (video, error) {
	row := a.db.QueryRow(`SELECT id, channel_id, owner_id, title, description, category, tags_json, visibility, status,
		thumbnail_url, playback_url, created_at, updated_at FROM videos WHERE id = ?`, id)
	return scanVideo(row)
}

func scanVideo(scanner interface{ Scan(...interface{}) error }) (video, error) {
	var item video
	var tagsJSON string
	err := scanner.Scan(&item.ID, &item.ChannelID, &item.OwnerID, &item.Title, &item.Description, &item.Category,
		&tagsJSON, &item.Visibility, &item.Status, &item.ThumbnailURL, &item.PlaybackURL, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return item, err
	}
	_ = json.Unmarshal([]byte(tagsJSON), &item.Tags)
	return item, nil
}

func scanVideos(rows *sql.Rows) ([]video, error) {
	items := make([]video, 0)
	for rows.Next() {
		item, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *application) getChannel(ctx context.Context, id string) (channelInfo, error) {
	var result channelInfo
	err := common.InternalJSON(ctx, http.MethodGet, a.authURL+"/internal/channels/"+url.PathEscape(id), a.serviceKey, nil, &result)
	return result, err
}

func positiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

var _ = fmt.Sprintf
