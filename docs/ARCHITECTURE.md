# Arquitectura de StreamSphere Go

## Adaptación tecnológica

La propuesta académica original se adapta a Go conservando separación por dominios, API Gateway, JWT, base de datos por servicio, resiliencia, observabilidad y comunicación distribuida.

| Concepto de arquitectura | Implementación en Go |
|---|---|
| API Gateway | Proxy inverso con `net/http/httputil` |
| Descubrimiento/configuración | Variables de entorno y direcciones de servicio |
| Circuit breaker | Estado de fallos y apertura temporal en Gateway |
| Seguridad distribuida | Middleware JWT y `X-Service-Key` |
| Salud y métricas | `/health` y `/metrics` |
| Persistencia por dominio | `database/sql` con SQLite independiente |
| Clientes internos | HTTP con timeout y contexto |
| Eventos | RabbitMQ opcional y respaldo HTTP |
| Procesamiento multimedia | FFmpeg |

## Componentes

```text
Navegador
   │
   ▼
Frontend HTML/CSS/JS
   │
   ▼
API Gateway
   ├── Auth User
   ├── Video Catalog
   ├── Media Processing ── FFmpeg ── MP4/JPG
   ├── Playback
   ├── Social Interaction
   ├── Analytics History
   └── Notification
```

Cada microservicio:

1. Se compila como un binario independiente.
2. Escucha el puerto definido en `PORT`.
3. Expone salud y métricas.
4. Mantiene su archivo SQLite de forma aislada.
5. Valida JWT o `X-Service-Key` según la ruta.
6. No consulta directamente la base de datos de otro dominio.

## Flujo de publicación

1. Auth emite un JWT para el creador.
2. Catalog crea el registro del video en `DRAFT`.
3. Media recibe el archivo y crea un trabajo `QUEUED`.
4. Catalog cambia el estado a `PROCESSING`.
5. FFmpeg genera una miniatura y un MP4 optimizado.
6. Catalog cambia el video a `PUBLISHED` y guarda las rutas.
7. Media publica el evento o ejecuta el respaldo HTTP.
8. Notification registra el aviso para el creador.
9. El frontend consulta el catálogo y reproduce mediante Playback.

## Comunicación

### Síncrona

- Gateway enruta solicitudes del navegador.
- Catalog consulta Auth para información de canales.
- Media actualiza Catalog y Notification.
- Playback consulta Catalog y registra Analytics.
- Social valida videos en Catalog y genera Notification.

### Asíncrona opcional

Si `RABBITMQ_URL` está configurada, los servicios utilizan el exchange `streamsphere.events` con eventos como:

- `video.processed`
- `video.failed`
- `comment.created`
- `reaction.created`
- `subscription.created`
- `playback.started`
- `playback.progress`
- `playback.completed`

## Resiliencia

- Los clientes internos utilizan contextos y timeouts.
- El Gateway abre el circuito después de tres fallos consecutivos.
- El circuito vuelve a probar el servicio después de 15 segundos.
- Un fallo de Notification o Analytics no debe bloquear la reproducción.
- El Gateway usa hasta 90 segundos para tolerar el arranque en frío de servicios gratuitos.
- `/health` verifica solo el Gateway; `/health/dependencies` revisa toda la plataforma.

## Persistencia

| Servicio | Datos propios |
|---|---|
| Auth | usuarios, roles, perfiles y canales |
| Catalog | videos, categorías, etiquetas y estados |
| Media | trabajos de procesamiento y archivos |
| Playback | eventos de reproducción |
| Social | comentarios, reacciones, playlists y suscripciones |
| Analytics | historial, vistas y tiempo visto |
| Notification | notificaciones y lectura |

Los UUID se comparten como referencias lógicas. No existen llaves foráneas físicas entre bases de datos diferentes.

## Despliegue en Render

`render.yaml` crea un servicio web por microservicio y un sitio estático para el frontend. Las direcciones se inyectan mediante variables de entorno. En el plan gratuito los servicios se comunican por sus direcciones HTTPS públicas.
