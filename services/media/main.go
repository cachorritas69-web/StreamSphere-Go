package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"streamsphere/internal/common"
)

const serviceName = "media-processing-service"

type application struct {
	db              *sql.DB
	jwtSecret       string
	serviceKey      string
	catalogURL      string
	notificationURL string
	mediaRoot       string
	publicBaseURL   string
	bus             common.EventBus
	maxUploadBytes  int64
}

type videoInfo struct {
	ID        string `json:"videoId"`
	OwnerID   string `json:"ownerId"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Playback  string `json:"playbackUrl"`
	Thumbnail string `json:"thumbnailUrl"`
}

type job struct {
	ID        string `json:"jobId"`
	VideoID   string `json:"videoId"`
	OwnerID   string `json:"ownerId"`
	Status    string `json:"status"`
	Progress  int    `json:"progress"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func main() {
	port := common.Env("PORT", "8083")
	db, err := common.OpenSQLite(common.Env("DB_PATH", "/data/media/media.db"))
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
		mediaRoot:       common.Env("MEDIA_ROOT", "/data/media/files"),
		publicBaseURL:   common.EnvURL("PUBLIC_BASE_URL", "http://localhost:8080"),
		bus:             common.EventBus{URL: common.Env("RABBITMQ_URL", "")},
		maxUploadBytes:  int64(common.EnvInt("MAX_UPLOAD_MB", 500)) * 1024 * 1024,
	}
	if err := os.MkdirAll(app.mediaRoot, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := app.migrate(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	metrics := common.NewMetrics(serviceName)
	mux.HandleFunc("GET /health", common.HealthHandler(serviceName))
	mux.HandleFunc("GET /metrics", metrics.Handler)
	mux.Handle("POST /api/media/videos/{id}/upload", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.upload)))
	mux.Handle("GET /api/media/jobs/{id}", common.Authenticate(app.jwtSecret, true)(http.HandlerFunc(app.getJob)))
	mux.Handle("/media/", http.StripPrefix("/media/", http.FileServer(http.Dir(app.mediaRoot))))

	handler := common.Chain(mux,
		common.CORS(common.Env("ALLOWED_ORIGIN", "*")), common.RequestID,
		common.Recover(serviceName), metrics.Middleware, common.Logging(serviceName),
	)
	server := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 15 * time.Second}
	log.Printf("%s listening on :%s", serviceName, port)
	log.Fatal(server.ListenAndServe())
}

func (a *application) migrate() error {
	return common.Migrate(a.db,
		`CREATE TABLE IF NOT EXISTS media_jobs (
			id TEXT PRIMARY KEY,
			video_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			status TEXT NOT NULL,
			progress INTEGER NOT NULL DEFAULT 0,
			error_message TEXT NOT NULL DEFAULT '',
			input_path TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_media_jobs_video ON media_jobs(video_id, created_at)`,
	)
}

func (a *application) upload(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	if !common.HasRole(claims, "CREATOR", "ADMIN") {
		common.Fail(w, http.StatusForbidden, "CREATOR_REQUIRED", "Solo un creador puede subir videos")
		return
	}
	videoID := r.PathValue("id")
	video, err := a.getVideo(r.Context(), videoID)
	if err != nil {
		common.Fail(w, http.StatusNotFound, "VIDEO_NOT_FOUND", "Video no encontrado")
		return
	}
	if video.OwnerID != claims.Subject && claims.Role != "ADMIN" {
		common.Fail(w, http.StatusForbidden, "NOT_VIDEO_OWNER", "No puedes subir archivos a este video")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.maxUploadBytes+(2<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		common.Fail(w, http.StatusBadRequest, "UPLOAD_TOO_LARGE", fmt.Sprintf("El archivo supera el límite de %d MB o el formulario es inválido", a.maxUploadBytes/(1024*1024)))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		common.Fail(w, http.StatusBadRequest, "FILE_REQUIRED", "Debes enviar un archivo en el campo file")
		return
	}
	defer file.Close()
	if err := validateVideoFile(header); err != nil {
		common.Fail(w, http.StatusBadRequest, "INVALID_VIDEO", err.Error())
		return
	}
	incomingDir := filepath.Join(a.mediaRoot, "incoming")
	if err := os.MkdirAll(incomingDir, 0o755); err != nil {
		common.Fail(w, http.StatusInternalServerError, "STORAGE_ERROR", "No se pudo preparar el almacenamiento")
		return
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	jobID := uuid.NewString()
	inputPath := filepath.Join(incomingDir, jobID+ext)
	if err := saveUploadedFile(file, inputPath, a.maxUploadBytes); err != nil {
		common.Fail(w, http.StatusInternalServerError, "UPLOAD_ERROR", err.Error())
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	created := job{ID: jobID, VideoID: videoID, OwnerID: claims.Subject, Status: "QUEUED", Progress: 0, CreatedAt: now, UpdatedAt: now}
	_, err = a.db.Exec(`INSERT INTO media_jobs(id, video_id, owner_id, status, progress, input_path, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		created.ID, created.VideoID, created.OwnerID, created.Status, created.Progress, inputPath, created.CreatedAt, created.UpdatedAt)
	if err != nil {
		_ = os.Remove(inputPath)
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo crear el trabajo de procesamiento")
		return
	}
	_ = a.updateCatalog(context.Background(), videoID, "PROCESSING", "", "")
	go a.process(created, video, inputPath)
	common.Created(w, "Archivo recibido y enviado a procesamiento", created)
}

func validateVideoFile(header *multipart.FileHeader) error {
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{".mp4": true, ".mov": true, ".mkv": true, ".webm": true, ".avi": true, ".m4v": true}
	if !allowed[ext] {
		return errors.New("Formato no permitido. Usa MP4, MOV, MKV, WEBM, AVI o M4V")
	}
	if header.Size == 0 {
		return errors.New("El archivo está vacío")
	}
	return nil
}

func saveUploadedFile(source multipart.File, path string, maxBytes int64) error {
	destination, err := os.Create(path)
	if err != nil {
		return err
	}
	defer destination.Close()
	written, err := io.Copy(destination, io.LimitReader(source, maxBytes+1))
	if err != nil {
		return err
	}
	if written > maxBytes {
		_ = os.Remove(path)
		return errors.New("el archivo supera el límite configurado")
	}
	return nil
}

func (a *application) process(created job, video videoInfo, inputPath string) {
	update := func(status string, progress int, message string) {
		_, _ = a.db.Exec(`UPDATE media_jobs SET status=?, progress=?, error_message=?, updated_at=? WHERE id=?`,
			status, progress, message, time.Now().UTC().Format(time.RFC3339), created.ID)
	}
	update("PROCESSING", 10, "")
	outputDir := filepath.Join(a.mediaRoot, "videos", created.VideoID)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		a.failJob(created, video, err)
		return
	}
	streamPath := filepath.Join(outputDir, "stream.mp4")
	thumbnailPath := filepath.Join(outputDir, "thumbnail.jpg")

	update("PROCESSING", 30, "")
	thumbnailCommand := exec.Command("ffmpeg", "-y", "-ss", "00:00:01", "-i", inputPath, "-frames:v", "1", "-vf", "scale=640:-2", thumbnailPath)
	if output, err := thumbnailCommand.CombinedOutput(); err != nil {
		log.Printf("thumbnail video=%s error=%v output=%s", created.VideoID, err, string(output))
	}

	update("PROCESSING", 55, "")
	transcodeCommand := exec.Command("ffmpeg", "-y", "-i", inputPath,
		"-vf", "scale='min(1280,iw)':-2", "-c:v", "libx264", "-preset", "veryfast", "-crf", "24",
		"-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", streamPath)
	if output, err := transcodeCommand.CombinedOutput(); err != nil {
		log.Printf("transcode video=%s error=%v output=%s", created.VideoID, err, string(output))
		a.failJob(created, video, fmt.Errorf("FFmpeg no pudo procesar el video: %w", err))
		return
	}
	update("PROCESSING", 90, "")
	playbackURL := "/media/videos/" + created.VideoID + "/stream.mp4"
	thumbnailURL := ""
	if _, err := os.Stat(thumbnailPath); err == nil {
		thumbnailURL = "/media/videos/" + created.VideoID + "/thumbnail.jpg"
	}
	if err := a.updateCatalog(context.Background(), created.VideoID, "PUBLISHED", thumbnailURL, playbackURL); err != nil {
		a.failJob(created, video, fmt.Errorf("no se pudo publicar en catálogo: %w", err))
		return
	}
	update("COMPLETED", 100, "")
	_ = os.Remove(inputPath)
	event := common.NewEvent("video.processed", serviceName, map[string]interface{}{
		"videoId": created.VideoID, "ownerId": created.OwnerID, "recipientId": created.OwnerID,
		"title": video.Title, "status": "PUBLISHED", "playbackUrl": playbackURL, "thumbnailUrl": thumbnailURL,
		"renditions": []string{"720p"},
	})
	a.bus.PublishBestEffort(event)
	a.notifyBestEffort(event, "VIDEO_READY", "Tu video ya está disponible", fmt.Sprintf("%s terminó de procesarse.", video.Title), "/?video="+created.VideoID)
}

func (a *application) failJob(created job, video videoInfo, processingError error) {
	message := processingError.Error()
	_, _ = a.db.Exec(`UPDATE media_jobs SET status='FAILED', error_message=?, updated_at=? WHERE id=?`, message, time.Now().UTC().Format(time.RFC3339), created.ID)
	_ = a.updateCatalog(context.Background(), created.VideoID, "FAILED", "", "")
	event := common.NewEvent("video.failed", serviceName, map[string]interface{}{
		"videoId": created.VideoID, "ownerId": created.OwnerID, "recipientId": created.OwnerID,
		"title": video.Title, "status": "FAILED", "error": message,
	})
	a.bus.PublishBestEffort(event)
	a.notifyBestEffort(event, "VIDEO_FAILED", "No se pudo procesar tu video", message, "/")
}

func (a *application) getJob(w http.ResponseWriter, r *http.Request) {
	claims, _ := common.ClaimsFromRequest(r)
	var found job
	err := a.db.QueryRow(`SELECT id, video_id, owner_id, status, progress, error_message, created_at, updated_at FROM media_jobs WHERE id=?`, r.PathValue("id")).Scan(
		&found.ID, &found.VideoID, &found.OwnerID, &found.Status, &found.Progress, &found.Error, &found.CreatedAt, &found.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		common.Fail(w, http.StatusNotFound, "JOB_NOT_FOUND", "Trabajo de procesamiento no encontrado")
		return
	}
	if err != nil {
		common.Fail(w, http.StatusInternalServerError, "DB_ERROR", "No se pudo consultar el trabajo")
		return
	}
	if found.OwnerID != claims.Subject && claims.Role != "ADMIN" {
		common.Fail(w, http.StatusForbidden, "NOT_JOB_OWNER", "No puedes consultar este trabajo")
		return
	}
	common.OK(w, "Estado del procesamiento", found)
}

func (a *application) getVideo(ctx context.Context, id string) (videoInfo, error) {
	var result videoInfo
	err := common.InternalJSON(ctx, http.MethodGet, a.catalogURL+"/internal/videos/"+id, a.serviceKey, nil, &result)
	return result, err
}

func (a *application) updateCatalog(ctx context.Context, id, status, thumbnailURL, playbackURL string) error {
	payload := map[string]string{"status": status, "thumbnailUrl": thumbnailURL, "playbackUrl": playbackURL}
	return common.InternalJSON(ctx, http.MethodPatch, a.catalogURL+"/internal/videos/"+id+"/processing", a.serviceKey, payload, nil)
}

func (a *application) notifyBestEffort(event common.Event, notificationType, title, message, link string) {
	payload := map[string]interface{}{
		"userId": event.Data["recipientId"], "type": notificationType, "title": title,
		"message": message, "link": link, "sourceEventId": event.EventID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := common.InternalJSON(ctx, http.MethodPost, a.notificationURL+"/internal/notifications", a.serviceKey, payload, nil); err != nil {
		log.Printf("notification fallback failed: %v", err)
	}
}
