# Guía de GitHub y Render

## 1. Verificar el proyecto localmente

Desde la raíz:

```bash
cp .env.example .env
make run
make smoke
```

Cuando la prueba termine correctamente:

```bash
make stop
```

## 2. Crear el repositorio en GitHub

En GitHub crea un repositorio vacío. Un nombre recomendado es:

```text
streamsphere-go-microservices
```

No agregues archivos desde el formulario de creación porque el proyecto ya incluye README, licencia y configuración.

## 3. Inicializar Git

```bash
git init
git add .
git status
git commit -m "Proyecto inicial StreamSphere en Go"
git branch -M main
```

Conecta el remoto:

```bash
git remote add origin https://github.com/TU_USUARIO/streamsphere-go-microservices.git
git push -u origin main
```

Para autenticación por SSH:

```bash
git remote add origin git@github.com:TU_USUARIO/streamsphere-go-microservices.git
git push -u origin main
```

## 4. Revisar GitHub Actions

Abre la pestaña **Actions** del repositorio y entra al flujo **CI StreamSphere**.

Debe completar:

1. Descarga del repositorio.
2. Configuración de Go.
3. Descarga de dependencias.
4. Verificación de formato.
5. `go vet ./...`.
6. `go test ./...`.
7. Compilación de todos los servicios.

No continúes al despliegue si el flujo aparece en rojo. Abre el paso fallido y revisa el mensaje exacto.

## 5. Crear el Blueprint en Render

1. Inicia sesión en Render.
2. Selecciona **New**.
3. Selecciona **Blueprint**.
4. Conecta GitHub cuando Render lo solicite.
5. Selecciona el repositorio de StreamSphere.
6. Render debe detectar automáticamente `render.yaml`.
7. Confirma los recursos y selecciona **Apply**.

Se crearán:

- `streamsphere-web-auth`
- `streamsphere-web-catalog`
- `streamsphere-web-notification`
- `streamsphere-web-analytics`
- `streamsphere-web-media`
- `streamsphere-web-playback`
- `streamsphere-web-social`
- `streamsphere-web-gateway`
- `streamsphere-web-frontend`

## 6. Orden de verificación

Cuando los despliegues terminen, abre primero:

```text
streamsphere-web-auth/health
streamsphere-web-catalog/health
streamsphere-web-media/health
streamsphere-web-playback/health
streamsphere-web-social/health
streamsphere-web-analytics/health
streamsphere-web-notification/health
streamsphere-web-gateway/health
```

Después abre la dirección de `streamsphere-web-frontend`.

El endpoint profundo del Gateway es:

```text
https://DOMINIO-DEL-GATEWAY/health/dependencies
```

## 7. Primera prueba en línea

1. Abre el frontend.
2. Inicia sesión con `demo@streamsphere.local` y `Demo123!`.
3. Entra a Studio.
4. Crea los metadatos de un video.
5. Sube un MP4 pequeño para la prueba inicial.
6. Espera a que el trabajo llegue a `COMPLETED`.
7. Regresa a Inicio y abre el video.
8. Publica un comentario y una reacción.
9. Abre Historial, Analítica y Notificaciones.

## 8. Cambios posteriores

Cada cambio sigue este flujo:

```bash
git checkout -b feature/nombre-del-cambio
# editar archivos
git add .
git commit -m "Descripción del cambio"
git push -u origin feature/nombre-del-cambio
```

Crea un Pull Request hacia `main`. Render está configurado para desplegar después de que las comprobaciones de GitHub terminen correctamente.

## 9. Problemas frecuentes

### El frontend muestra error de conexión

Revisa el log de construcción de `streamsphere-web-frontend`. Debe mostrar la API configurada con el hostname de `streamsphere-web-gateway`.

### Un endpoint tarda demasiado la primera vez

Los servicios gratuitos pueden estar suspendidos. Abre sus rutas `/health` y espera a que todos respondan antes de repetir la operación.

### Los usuarios o videos desaparecieron

La persistencia local del plan gratuito es temporal. Un reinicio, suspensión o nuevo despliegue elimina los archivos SQLite y los videos cargados.

### Media Service falla al procesar

- Usa inicialmente un MP4 pequeño.
- Revisa el log del servicio Media.
- Confirma que el archivo no supera `MAX_UPLOAD_MB`.
- Revisa el mensaje del trabajo desde `/api/media/jobs/{jobId}`.

### Render indica que el nombre de un servicio ya existe

Cambia todos los nombres de `render.yaml` manteniendo consistentes las referencias `fromService.name`. Usa un prefijo único, por ejemplo tus iniciales o el nombre del equipo.
