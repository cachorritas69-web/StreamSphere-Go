# Entrega rápida: GitHub y Render

## 1. Probar el proyecto en Arch Linux

```bash
bash scripts/install-arch.sh
cp .env.example .env
make run
```

Abre `http://localhost:3000`. Para detenerlo:

```bash
make stop
```

## 2. Crear el repositorio en GitHub

Crea un repositorio vacío. No agregues archivos desde la página de GitHub.

Desde la raíz del proyecto:

```bash
git init
git add .
git commit -m "Proyecto inicial StreamSphere en Go"
git branch -M main
git remote add origin https://github.com/TU_USUARIO/TU_REPOSITORIO.git
git push -u origin main
```

Revisa la pestaña **Actions** y confirma que el flujo `CI StreamSphere` termine correctamente.

## 3. Crear los servicios en Render

1. Entra al panel de Render.
2. Selecciona **New > Blueprint**.
3. Conecta GitHub y elige el repositorio.
4. Render detectará el archivo `render.yaml`.
5. Revisa los nueve componentes y pulsa **Apply**.
6. Espera a que todos muestren estado disponible.
7. Abre `streamsphere-web-frontend`.

## 4. Comprobaciones

- Registro e inicio de sesión.
- Creación de canal.
- Creación de metadatos de video.
- Carga y procesamiento de un video corto.
- Reproducción del video.
- Comentario, reacción y suscripción.
- Historial, analítica y notificaciones.

## 5. Cuando algo falle

Guarda una captura y copia las últimas líneas del registro del servicio que falló. Revisa primero:

- Que la compilación de GitHub Actions esté en verde.
- Que todos los servicios de Render estén disponibles.
- Que los nombres del archivo `render.yaml` no hayan sido cambiados parcialmente.
- Que la URL abierta corresponda a `streamsphere-web-frontend`.
