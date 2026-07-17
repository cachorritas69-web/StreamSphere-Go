# StreamSphere Web en Go

StreamSphere es un MVP académico de una plataforma de streaming de video tipo YouTube. El backend está dividido en microservicios independientes escritos en Go y la interfaz está hecha únicamente con HTML, CSS y JavaScript.

El repositorio está preparado para:

- Ejecutarse localmente desde terminal.
- Subirse directamente a GitHub.
- Validarse con GitHub Actions.
- Desplegarse en Render mediante el archivo `render.yaml`.

## Funcionalidades

- Registro e inicio de sesión con JWT.
- Perfiles, roles y creación de canales.
- Catálogo y búsqueda de videos.
- Estados `DRAFT`, `PROCESSING`, `PUBLISHED` y `FAILED`.
- Carga de archivos y procesamiento con FFmpeg.
- Generación de miniatura y MP4 para reproducción web.
- Comentarios, likes, dislikes, suscripciones y playlists.
- Historial, vistas, progreso y analítica de creador.
- Notificaciones internas.
- API Gateway con proxy inverso, timeouts y circuit breaker.
- Base de datos independiente por servicio mediante SQLite.
- Endpoints `/health` y `/metrics`.
- Contratos principales en `docs/openapi.yaml`.

## Arquitectura

| Componente | Puerto local | Responsabilidad |
|---|---:|---|
| Frontend | 3000 | Interfaz HTML, CSS y JavaScript |
| API Gateway | 8080 | Entrada central y enrutamiento |
| Auth User | 8081 | Usuarios, JWT, perfiles y canales |
| Video Catalog | 8082 | Metadatos, búsqueda y estados |
| Media Processing | 8083 | Carga, FFmpeg y archivos multimedia |
| Playback | 8084 | URL de reproducción y eventos |
| Social Interaction | 8085 | Comentarios, reacciones, suscripciones y playlists |
| Analytics History | 8086 | Historial, vistas y métricas |
| Notification | 8087 | Notificaciones y estados de lectura |

Cada microservicio se compila como un binario independiente, mantiene su propia base de datos y se comunica mediante HTTP. El soporte de RabbitMQ queda disponible de forma opcional mediante `RABBITMQ_URL`; si no existe un broker configurado, los flujos principales utilizan llamadas internas de respaldo.

## Requisitos locales

- Go 1.23 o superior.
- GCC, requerido por el controlador SQLite.
- FFmpeg para procesar videos.
- Git, Make y curl.

### Instalación rápida en Arch Linux

```bash
bash scripts/install-arch.sh
```

## Ejecutar localmente

### 1. Preparar variables

```bash
cp .env.example .env
```

Cambia `JWT_SECRET` y `SERVICE_KEY` dentro de `.env`.

### 2. Iniciar toda la plataforma

```bash
make run
```

El script descarga módulos, compila los nueve binarios, inicia los procesos y verifica su salud.

Abre:

```text
http://localhost:3000
```

### 3. Detener los procesos

```bash
make stop
```

### Otros comandos

```bash
make build     # Compilar todos los servicios
make test      # Ejecutar pruebas y análisis estático
make health    # Revisar las dependencias desde el Gateway
make smoke     # Probar registro, JWT, canal y catálogo
make clean     # Eliminar archivos generados localmente
```

Los logs locales se guardan en `.run/logs/`.

## Usuarios precargados

| Rol | Correo | Contraseña |
|---|---|---|
| Creador | `demo@streamsphere.local` | `Demo123!` |
| Administrador | `admin@streamsphere.local` | `Admin123!` |

Estos usuarios se crean automáticamente cuando Auth Service inicia con una base vacía.

## Subir el proyecto a GitHub

Crea primero un repositorio vacío en GitHub, sin README ni licencia generados por la página. Luego ejecuta desde la carpeta del proyecto:

```bash
git init
git add .
git commit -m "Proyecto inicial StreamSphere en Go"
git branch -M main
git remote add origin https://github.com/TU_USUARIO/TU_REPOSITORIO.git
git push -u origin main
```

Al hacer `push`, el flujo `.github/workflows/ci.yml` revisa formato, ejecuta `go vet`, corre las pruebas y compila todos los servicios.

## Desplegar en Render

El archivo `render.yaml` define ocho servicios web en Go y un sitio estático para el frontend.

1. Sube el proyecto a GitHub.
2. En Render selecciona **New > Blueprint**.
3. Conecta tu cuenta de GitHub y selecciona el repositorio.
4. Confirma que Render detectó `render.yaml`.
5. Pulsa **Apply** y espera a que terminen las compilaciones.
6. Abre el servicio `streamsphere-web-frontend`.

Render genera automáticamente `JWT_SECRET` y `SERVICE_KEY` compartidos. También conecta las direcciones públicas de los microservicios usando `RENDER_EXTERNAL_HOSTNAME`.

La guía ampliada está en [`docs/GITHUB_RENDER.md`](docs/GITHUB_RENDER.md).

### Consideraciones del plan gratuito de Render

Esta configuración sirve para una demostración académica, no para producción:

- Los servicios gratuitos pueden suspenderse tras un periodo sin tráfico y tardar en despertar.
- SQLite y los videos guardados localmente se pierden cuando una instancia se reinicia, se vuelve a desplegar o se suspende.
- Ocho servicios activos consumen las horas gratuitas con mayor rapidez.
- El primer acceso puede tardar mientras despiertan el Gateway y sus dependencias.

Para una versión con datos permanentes se deben migrar las bases a un servicio administrado y los videos a almacenamiento de objetos.

## Variables principales

| Variable | Uso |
|---|---|
| `PORT` | Puerto asignado a cada proceso |
| `JWT_SECRET` | Firma de tokens JWT |
| `SERVICE_KEY` | Autenticación de endpoints internos |
| `DB_PATH` | Archivo SQLite exclusivo del servicio |
| `ALLOWED_ORIGIN` | Origen autorizado para CORS |
| `PUBLIC_BASE_URL` | Dirección pública del Gateway |
| `MAX_UPLOAD_MB` | Tamaño máximo de carga |
| `RABBITMQ_URL` | Broker opcional para eventos |
| `*_SERVICE_URL` | Dirección de otro microservicio |

## Rutas principales

```text
POST   /api/auth/register
POST   /api/auth/login
GET    /api/users/me
POST   /api/channels
GET    /api/channels/me

POST   /api/videos
GET    /api/videos/search
GET    /api/videos/mine
GET    /api/videos/{id}
PATCH  /api/videos/{id}
PATCH  /api/videos/{id}/visibility

POST   /api/media/videos/{id}/upload
GET    /api/media/jobs/{jobId}
GET    /api/playback/videos/{id}/manifest
POST   /api/playback/events

GET    /api/videos/{id}/comments
POST   /api/videos/{id}/comments
POST   /api/videos/{id}/reactions
POST   /api/channels/{id}/subscriptions
POST   /api/playlists
GET    /api/playlists/me

GET    /api/history/me
GET    /api/analytics/creators/me
GET    /api/notifications/me
PATCH  /api/notifications/{id}/read
```

## Estructura

```text
StreamSphere-Go-GitHub-Render/
├── .github/
│   └── workflows/ci.yml
├── frontend/
│   ├── index.html
│   ├── styles.css
│   ├── app.js
│   └── config.js
├── internal/common/
├── services/
│   ├── analytics/
│   ├── auth/
│   ├── catalog/
│   ├── frontend/
│   ├── gateway/
│   ├── media/
│   ├── notification/
│   ├── playback/
│   └── social/
├── docs/
├── scripts/
├── render.yaml
├── Makefile
└── go.mod
```

## Limitaciones del MVP

- El procesamiento crea una versión MP4 720p, no un paquete HLS multirresolución.
- El circuit breaker es una implementación ligera dentro del Gateway.
- La mensajería externa es opcional y no se crea automáticamente en Render.
- No incluye streaming en vivo, monetización, CDN real ni moderación automática.
- La persistencia local de Render es temporal en el plan gratuito.

## Licencia

Proyecto académico distribuido bajo la licencia MIT.
