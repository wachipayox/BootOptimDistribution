# Guía de uso y mantenimiento de BootOptim Distribution

Guía práctica para administrar el servicio privado de distribución de perfiles de Wachiland Elite. Está pensada para el servidor actual y distingue sus rutas conocidas de las instrucciones generales. No contiene contraseñas, claves privadas ni tokens.

**Última revisión:** 1 de octubre de 2026

**Servidor conocido:** `wachilandserver` · Ubuntu/Debian · IP LAN `192.168.1.69`

**Última versión publicada en esta guía:** `0.2.18`. Comprueba siempre la versión instalada con los comandos de esta guía.

## Índice

1. [Qué hace el servicio](#qué-hace-el-servicio)
2. [Rutas y accesos actuales](#rutas-y-accesos-actuales)
3. [Uso del panel y publicación](#uso-del-panel-y-publicación)
4. [Las tres credenciales que no hay que confundir](#las-tres-credenciales-que-no-hay-que-confundir)
5. [Copia, recuperación y rotación de la clave de firma](#copia-recuperación-y-rotación-de-la-clave-de-firma)
6. [Administrar el servicio Linux](#administrar-el-servicio-linux)
7. [Cambiar la configuración de systemd](#cambiar-la-configuración-de-systemd)
8. [Diagnóstico de problemas](#diagnóstico-de-problemas)
9. [Actualizar y volver atrás](#actualizar-y-volver-atrás)
10. [Desactivar o desinstalar](#desactivar-o-desinstalar)
11. [Lista periódica de mantenimiento](#lista-periódica-de-mantenimiento)

## Qué hace el servicio

Distribution guarda revisiones globales inmutables, valida sus firmas y almacena los archivos publicados como objetos identificados por SHA-256. El panel permite preparar y publicar perfiles; el launcher consulta las revisiones firmadas que tiene permitidas.

El servidor firma automáticamente al publicar desde el panel autenticado. La clave privada se genera una vez y permanece en el directorio de datos de Linux; el navegador y el launcher sólo reciben claves públicas y firmas. No necesitas guardar ni usar claves en tu PC. El administrador puede instalar y reiniciar las actualizaciones desde Servicio; la recuperación manual sigue disponible.

La interfaz administrativa actual está en `https://welite.ddns.net:8444/admin/`. En la LAN, el nombre `welite.ddns.net` debe resolver a `192.168.1.69` para que el certificado Certbot coincida. No uses la URL por IP como dirección habitual porque el certificado es para el nombre DNS.

El proceso escucha en `192.168.1.69:8444`, restringe clientes a `192.168.1.0/24` y usa HTTPS nativo. No necesita Caddy ni un proxy Nginx. No reenvíes este puerto desde Internet.

## Rutas y accesos actuales

| Elemento | Ruta o valor conocido |
|---|---|
| Checkout Linux | `/home/wachi/launcher_manager` |
| Binario que ejecuta systemd con el actualizador habilitado | `/usr/local/bin/bootoptim-distribution` |
| Copia del binario del checkout | `/home/wachi/launcher_manager/bin/bootoptim-distribution` |
| Unidad systemd | `/etc/systemd/system/bootoptim-distribution.service` |
| Datos publicados y objetos | `/var/lib/bootoptim-distribution` |
| Clave automática del servidor (privada, no compartir) | `/var/lib/bootoptim-distribution/signing/server-release-key.json` |
| Historial automático de claves públicas | `/var/lib/bootoptim-distribution/signing/public-keys.json` |
| Mapa de claves públicas de firma anteriores | `/etc/bootoptim-distribution/release-public-keys.json` |
| Verificador de contraseña admin | `/etc/bootoptim-distribution/admin-password.hash` |
| Certificado TLS Certbot | `/etc/letsencrypt/live/welite.ddns.net/fullchain.pem` |
| Clave privada TLS Certbot | `/etc/letsencrypt/live/welite.ddns.net/privkey.pem` |
| Usuario/grupo del servicio | `wachi` / `wachi` |
| Bind / puerto / CIDR LAN | `192.168.1.69:8444` / `192.168.1.0/24` |
| Panel | `https://welite.ddns.net:8444/admin/` |

Las rutas de Certbot son gestionadas por Certbot y Nginx; no cambies sus permisos ni copies los archivos a mano. La unidad usa credenciales de systemd para entregarlas al proceso sin abrir los directorios privados de Certbot.

El instalador y el actualizador mantienen el binario del checkout y el binario root-owned `/usr/local/bin/bootoptim-distribution`; la unidad ejecuta este último después de instalar el actualizador del panel. Se conservan copias `.previous` de ambos. El directorio de datos, `/etc/bootoptim-distribution` y el certificado son independientes del checkout y no se borran al actualizar el código.

## Uso del panel y publicación

1. Abre `https://welite.ddns.net:8444/admin/` desde la LAN e inicia sesión con el usuario administrador configurado (`wachi`) y la contraseña elegida al crear el verificador. No hay una contraseña predeterminada.
2. En Perfiles globales, crea o selecciona el perfil y prepara el contenido/revisión. Para una hija, comprueba la versión base y los permisos heredados antes de publicar.
3. Revisa los archivos y reglas y pulsa **Preparar archivos**. Sólo se suben los objetos necesarios.
4. Marca que has revisado los cambios y pulsa **Publicar versión**. Confirma la publicación. El servidor valida, firma y almacena la revisión automáticamente.
5. Comprueba el historial. No hay descarga de solicitudes ni subida de archivos firmados.

Las revisiones publicadas son inmutables; una corrección del contenido crea una secuencia posterior. Nombre, descripción e icono se cambian con **Editar perfil**, sin crear una revisión del juego.

### Borrar un perfil global

En **Perfiles globales**, pulsa **Borrar perfil**. El diálogo muestra el nombre
y el identificador del perfil. Escribe el identificador completo y confirma;
si no coincide, el botón permanece desactivado. **Cancelar** no cambia nada.

El perfil desaparece del catálogo de la web y el launcher. Las instancias ya
instaladas y los perfiles derivados se conservan. El servidor guarda su
historial firmado para no romper referencias heredadas, y reserva su ID para
que no pueda reutilizarse accidentalmente. Este botón no borra mundos ni
archivos locales. No hay un botón de restauración en esta versión; conserva
las copias de seguridad completas del servicio.

### Comprobar servicio y catálogo

Desde Linux:

```bash
curl -fsS https://welite.ddns.net:8444/healthz
curl -fsS https://welite.ddns.net:8444/v1/meta/version
curl -fsS https://welite.ddns.net:8444/v1/profiles
```

`/v1/meta/version` muestra versión, commit y capacidades. `/v1/profiles` puede devolver una lista vacía si aún no hay revisiones publicadas; eso no significa que el proceso esté roto.

## Las tres credenciales que no hay que confundir

### 1. Clave privada de firma de perfiles

Es Ed25519 y firma las revisiones del modpack. El servicio la crea automáticamente en `/var/lib/bootoptim-distribution/signing/server-release-key.json`, con permisos de propietario. No la descargues ni la copies al navegador.

Las claves públicas anteriores siguen en `/etc/bootoptim-distribution/release-public-keys.json`; las nuevas y su historial se conservan en `signing/public-keys.json`. El launcher consulta `/v1/signing-keys` mediante HTTPS verificado y conserva la compatibilidad con claves ya configuradas.

### 2. Verificador de contraseña del panel

Está en `/etc/bootoptim-distribution/admin-password.hash`. Es un hash PBKDF2, no la contraseña recuperable. Sirve sólo para el login del panel; no firma perfiles. La unidad ejecuta el servicio como `wachi`, así que el archivo debe poder leerlo ese usuario y no otros usuarios ordinarios.

### 3. Clave privada TLS del sitio

Es la clave de Certbot `privkey.pem` para HTTPS. No firma perfiles y no es la contraseña del panel. systemd la carga con `LoadCredential=` al iniciar el proceso. No alteres los permisos de `/etc/letsencrypt` para intentar conceder acceso directo a `wachi`.

## Copia, recuperación y rotación de la clave de firma

### Copia de seguridad del servidor

La copia debe incluir **todo** `/var/lib/bootoptim-distribution` (base de datos, objetos e identidad de firma) y `/etc/bootoptim-distribution` (verificador y claves públicas anteriores). Guarda el archivo en un almacenamiento privado, preferiblemente cifrado, separado del servidor. Contiene información privada; no lo subas al repositorio.

Para una copia consistente, detén el servicio brevemente:

```bash
sudo systemctl stop bootoptim-distribution
sudo tar -czpf /root/bootoptim-backup-$(date +%Y%m%d-%H%M%S).tar.gz /var/lib/bootoptim-distribution /etc/bootoptim-distribution
sudo systemctl start bootoptim-distribution
```

Comprueba que volvió a quedar activo. Si `tar` falla, reinicia igualmente el servicio y repite la copia cuando hayas resuelto el error. Conserva también la unidad y sus drop-ins; sus rutas aparecen en la sección de systemd.

### Si pierdes o cambias el PC

Abre el panel desde el nuevo PC e inicia sesión. Puedes publicar nuevas revisiones de los mismos perfiles y crear otros. No necesitas recuperar una clave del PC. Instala la versión nueva del launcher para usar publicaciones automáticas.

### Si recuperas el servidor desde una copia

Detén el servicio, restaura los directorios anteriores con sus propietarios y permisos y vuelve a iniciarlo. Usa la misma configuración de HTTPS y directorio de datos. La identidad, perfiles e historial continuarán siendo los mismos. No mezcles una base de datos reciente con objetos de una copia antigua.

### Si se pierde sólo la clave privada del servidor

Si `server-release-key.json` falta, el siguiente arranque genera otra identidad. Conserva `signing/public-keys.json` y el mapa de claves públicas anterior: permiten verificar revisiones históricas. Se pueden publicar secuencias nuevas para los mismos IDs de perfil. Un archivo privado corrupto provoca un error explícito al arrancar; no se reemplaza silenciosamente. Antes de retirarlo para regenerar, guarda una copia completa del estado y confirma que está realmente corrupto.

La regeneración no recupera perfiles, objetos ni una base de datos perdidos: para eso necesitas la copia completa. No borres claves públicas antiguas. La clave TLS y la contraseña admin cumplen funciones distintas.

### Cambiar la contraseña administrativa

Si olvidaste la contraseña, no se puede leer desde el hash; reemplaza el verificador por uno nuevo. Este comando solicita la contraseña sin mostrarla ni incluirla en el historial del shell:

```bash
sudo python3 - <<'PY'
import base64, getpass, hashlib, os, pwd, tempfile

path = "/etc/bootoptim-distribution/admin-password.hash"
directory = os.path.dirname(path)
password = getpass.getpass("Nueva contraseña admin: ").encode("utf-8")
salt = os.urandom(16)
rounds = 600000
digest = hashlib.pbkdf2_hmac("sha256", password, salt, rounds, dklen=32)
b64 = lambda value: base64.urlsafe_b64encode(value).rstrip(b"=").decode("ascii")
value = f"$bootoptim$pbkdf2-sha256${rounds}${b64(salt)}${b64(digest)}\n"
account = pwd.getpwnam("wachi")
fd, temporary = tempfile.mkstemp(prefix=".admin-password.", dir=directory)
try:
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, "w", encoding="ascii") as output:
        output.write(value)
    os.chown(temporary, account.pw_uid, account.pw_gid)
    os.replace(temporary, path)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
PY
sudo systemctl restart bootoptim-distribution.service
```

Después inicia sesión con el usuario configurado (`wachi`) y la nueva contraseña. No publiques el valor del hash en un mensaje. El reinicio invalida las sesiones activas.

## Administrar el servicio Linux

### Estado, arranque y parada

```bash
sudo systemctl status bootoptim-distribution.service --no-pager --full
sudo systemctl is-active bootoptim-distribution.service
sudo systemctl start bootoptim-distribution.service
sudo systemctl stop bootoptim-distribution.service
sudo systemctl restart bootoptim-distribution.service
sudo systemctl enable bootoptim-distribution.service
```

`enable` configura el arranque automático; `start` inicia ahora; `restart` vuelve a leer el binario y credenciales. Para cambios en la unidad, primero ejecuta `daemon-reload` (sección siguiente).

### Ver logs y configuración efectiva

```bash
sudo journalctl -u bootoptim-distribution.service -n 100 --no-pager
sudo journalctl -u bootoptim-distribution.service -f
sudo systemctl cat bootoptim-distribution.service
sudo systemctl show bootoptim-distribution.service \
  -p FragmentPath -p DropInPaths -p ExecStart -p User -p Group --no-pager
sudo systemd-analyze verify /etc/systemd/system/bootoptim-distribution.service
```

`systemctl cat` enseña la unidad base y sus overrides. `systemctl show` presenta la configuración efectiva. Para comprobar red y firewall:

Si la búsqueda de versión falla en el panel con `update_check_failed`, consulta el error detallado que registra el servicio:

```bash
sudo journalctl -u bootoptim-distribution.service --since '-10 minutes' --no-pager | grep 'admin service update'
```

```bash
sudo ss -ltnp | grep ':8444'
sudo ufw status numbered
```

Si UFW está activo, la regla actual debe permitir TCP/8444 sólo desde la LAN:

```bash
sudo ufw allow from 192.168.1.0/24 to 192.168.1.69 port 8444 proto tcp
```

No añadas una regla global para cualquier origen. Si modificas o borras una regla UFW, usa el número que muestre `ufw status numbered` y revisa de nuevo el resultado.

### Renovación del certificado TLS

La unidad obtiene certificado y clave mediante `LoadCredential`. Tras renovar Certbot, reinicia el servicio para que systemd vuelva a cargar el certificado:

```bash
sudo systemctl restart bootoptim-distribution.service
sudo systemctl status bootoptim-distribution.service --no-pager
```

No copies archivos a `/home/wachi`, no cambies el propietario/modo de `/etc/letsencrypt` y no añadas un hook que los copie. Si el servicio falla al abrir un certificado, inspecciona la unidad y los logs; comprueba también que `LoadCredential` apunte a las rutas actuales.

## Cambiar la configuración de systemd

La unidad activa está en `/etc/systemd/system/bootoptim-distribution.service`. Su `ExecStart` conocido es equivalente a:

```ini
ExecStart=/home/wachi/launcher_manager/bin/bootoptim-distribution --listen 192.168.1.69:8444 --admin-ui-https --admin-ui-allow-cidr 192.168.1.0/24 --tls-cert-file=${ADMIN_TLS_CERT} --tls-key-file=${ADMIN_TLS_KEY} --admin-username wachi --admin-password-hash-file /etc/bootoptim-distribution/admin-password.hash --data-dir /var/lib/bootoptim-distribution --release-public-keys-file /etc/bootoptim-distribution/release-public-keys.json
```

La unidad también configura `LoadCredential=admin-cert:...`, `LoadCredential=admin-key:...` y las variables `ADMIN_TLS_CERT`/`ADMIN_TLS_KEY`. No elimines esas tres piezas mientras los argumentos TLS las usen.

Para editar, primero guarda una copia y revisa tanto la unidad como los drop-ins:

```bash
sudo cp -a /etc/systemd/system/bootoptim-distribution.service \
  /etc/systemd/system/bootoptim-distribution.service.backup
sudo systemctl cat bootoptim-distribution.service
sudo nano /etc/systemd/system/bootoptim-distribution.service
```

Al redefinir `ExecStart` en una unidad o drop-in, systemd requiere limpiar el valor anterior con una línea vacía `ExecStart=` y luego definir **una sola** línea `ExecStart=/ruta/al/binario ...`. Esa línea vacía es intencional; el error aparece si quedan dos `ExecStart` no vacíos. No uses un reemplazo global que pueda duplicar la línea. En un drop-in creado con `sudo systemctl edit bootoptim-distribution`, conserva el bloque completo de TLS/credenciales y un único comando de arranque.

Después de editar:

```bash
sudo systemd-analyze verify /etc/systemd/system/bootoptim-distribution.service
sudo systemctl daemon-reload
sudo systemctl restart bootoptim-distribution.service
sudo systemctl status bootoptim-distribution.service --no-pager --full
sudo journalctl -u bootoptim-distribution.service -n 50 --no-pager
```

Si `systemctl restart` falla, no repitas reinicios a ciegas. Mira `systemctl cat`, `systemctl show ... -p ExecStart` y el journal; restaura la copia conocida y ejecuta `daemon-reload` si es necesario.

Cambiar el puerto requiere actualizar `--listen`, la regla UFW y la URL utilizada por los administradores. Cambiar la IP requiere ajustar bind, CIDR/firewall y resolución DNS. Mantén el bind en la IP LAN explícita; no uses `0.0.0.0` ni el puerto `8443`, que ya está ocupado por Nginx.

## Diagnóstico de problemas

| Síntoma | Comprobaciones iniciales |
|---|---|
| Servicio `failed` | `systemctl status` y `journalctl -u ... -n 100`; comprobar `ExecStart`, `systemd-analyze verify` y permisos de los archivos citados. |
| `bad-setting` / “more than one ExecStart” | `systemctl cat` y `systemctl show -p ExecStart`; deja sólo un comando efectivo no vacío tras el reset `ExecStart=`. Ejecuta `daemon-reload`. |
| `permission denied` con Certbot | Verifica `LoadCredential` y las rutas. No abras los directorios de Certbot a `wachi` ni copies la clave. |
| Login incorrecto | Usuario configurado (`wachi`), contraseña introducida y ruta/hash legible. Reemplaza el verificador con el procedimiento anterior si se perdió la contraseña. |
| Panel no conecta | Confirma servicio activo, escucha en `192.168.1.69:8444`, regla LAN UFW, DNS local `welite.ddns.net → 192.168.1.69` y que el cliente esté en `192.168.1.0/24`. |
| El perfil no aparece | Consulta historial de perfiles y `/v1/profiles`; comprueba que la revisión se publicó y que el sobre fue firmado con un ID de clave confiable. |
| Cliente rechaza una firma | Comprueba HTTPS y `/v1/signing-keys`, conserva el historial público y actualiza el launcher a una versión con descubrimiento automático. |

No publiques capturas con cookies, tokens CSRF, hashes de contraseña, contenido de claves privadas o solicitudes de firma que puedan contener datos del perfil.

## Actualizar y volver atrás

El actualizador sigue `origin/main`, no `agent/integration-current` ni ramas de trabajo. En el panel autenticado, abre **Servicio → Actualización del servicio**. **Buscar actualización** compara el commit ejecutado con el tip público de `main`; **Actualizar y reiniciar** instala ese tip y reinicia la unidad al acabar.

Si la consulta de GitHub falla, la web muestra un mensaje genérico y el journal conserva el detalle técnico. Revisa ese registro antes de cambiar la unidad o la conectividad.

La primera actualización manual después de instalar esta versión configura el ejecutor automáticamente si `bootoptim-distribution.service` ya existe. Para instalarlo o reparar su configuración por separado, ejecuta:

```bash
sudo /home/wachi/launcher_manager/scripts/install-admin-updater.sh
```

Esto instala una unidad oneshot root, protegida por `flock`, y un socket Unix propiedad de la cuenta definida en `User=`. El proceso web sólo puede enviar la petición fija de actualizar; no recibe permisos generales de root ni una consola. La configuración conserva los argumentos actuales y cambia `ExecStart` a `/usr/local/bin/bootoptim-distribution`. Las futuras versiones se compilan desde un checkout temporal root-owned. Si el panel no está disponible, conserva este método manual de recuperación:

```bash
sudo /home/wachi/launcher_manager/scripts/update.sh --check
sudo /home/wachi/launcher_manager/scripts/update.sh --apply
```

`--check` compara el commit del servicio con `origin/main`. Después de habilitar el actualizador web, lee el marcador root-owned de `/var/lib/bootoptim-distribution-updater/installed-commit`; antes de eso usa `.installed-commit` en el checkout. `--apply` hace fetch, restablece el checkout al tip de `main`, compila e instala el binario. **El reset descarta cambios rastreados locales en ese checkout**; no guardes configuración manual dentro del repositorio. La configuración real del servicio, las claves y los datos están fuera del checkout.

Al terminar, reinicia el servicio explícitamente y verifica versión/estado:

```bash
sudo systemctl restart bootoptim-distribution.service
sudo systemctl status bootoptim-distribution.service --no-pager --full
curl -fsS https://welite.ddns.net:8444/v1/meta/version
```

El script mantiene `bin/bootoptim-distribution.previous` y `/usr/local/bin/bootoptim-distribution.previous`. Con el actualizador habilitado, la unidad activa usa `/usr/local/bin/bootoptim-distribution`. Para restaurar manualmente el anterior, detén el servicio, conserva primero el binario actual y copia el anterior sobre el ejecutado:

```bash
sudo systemctl stop bootoptim-distribution.service
sudo cp -a /home/wachi/launcher_manager/bin/bootoptim-distribution \
  /home/wachi/launcher_manager/bin/bootoptim-distribution.failed
sudo cp -a /home/wachi/launcher_manager/bin/bootoptim-distribution.previous \
  /home/wachi/launcher_manager/bin/bootoptim-distribution
sudo systemctl start bootoptim-distribution.service
sudo systemctl status bootoptim-distribution.service --no-pager --full
```

Esto revierte el ejecutable, no el esquema ni los datos publicados. Antes de volver atrás por una migración, revisa la versión y las notas de la actualización; haz copia del directorio de datos antes de operaciones que puedan cambiar el formato.

Si una versión antigua del actualizador falla con `Read-only file system` al
intentar instalar `50-admin-updater-binary.conf` o una unidad bajo
`/etc/systemd/system`, no amplíes los permisos de escritura del servicio
oneshot. Ejecuta una actualización manual una sola vez desde el checkout:

```bash
sudo /home/wachi/launcher_manager/scripts/update.sh --apply
sudo systemctl restart bootoptim-distribution.service
```

El instalador manual puede actualizar el helper fuera del sandbox. Las
siguientes versiones no reescriben unidades `systemd` desde el actualizador y
se pueden instalar otra vez desde el panel.

## Desactivar o desinstalar

### Parar temporalmente

```bash
sudo systemctl stop bootoptim-distribution.service
```

Para que no arranque al reiniciar:

```bash
sudo systemctl disable bootoptim-distribution.service
```

Para reactivarlo:

```bash
sudo systemctl enable --now bootoptim-distribution.service
```

### Quitar sólo la unidad de systemd y conservar el servicio/datos

Primero confirma la ruta efectiva y guarda una copia:

```bash
sudo systemctl cat bootoptim-distribution.service
sudo systemctl disable --now bootoptim-distribution.service
sudo cp -a /etc/systemd/system/bootoptim-distribution.service \
  /etc/systemd/system/bootoptim-distribution.service.backup
```

Si vas a desinstalarla, elimina sólo los archivos de unidad/drop-in que acabas de inspeccionar; luego recarga systemd:

```bash
sudo rm /etc/systemd/system/bootoptim-distribution.service
sudo systemctl daemon-reload
sudo systemctl reset-failed bootoptim-distribution.service
```

Si `systemctl cat` mostraba un directorio `bootoptim-distribution.service.d`, inspecciónalo y respáldalo antes de retirar individualmente sus overrides. No borres directorios de configuración o datos como parte de este paso.

### Borrar datos de perfiles o claves

`/var/lib/bootoptim-distribution` contiene revisiones/objetos publicados; `/etc/bootoptim-distribution` contiene credenciales y claves públicas. Borrarlos es una operación destructiva independiente de desinstalar systemd. Antes de hacerlo, detén el servicio, confirma que realmente quieres perder esos datos, crea y verifica una copia de seguridad y revisa la ruta exacta. No ejecutes `rm -rf` sobre rutas construidas o no verificadas. Certificados de `/etc/letsencrypt` pueden ser compartidos por Nginx/Pterodactyl; no los elimines al retirar Distribution.

## Lista periódica de mantenimiento

- Comprobar `systemctl is-active` y revisar errores recientes del journal.
- Consultar `/v1/meta/version` después de una actualización.
- Probar que el login del panel funciona y que DNS local apunta al servidor correcto.
- Verificar espacio libre y realizar copia consistente de `/var/lib/bootoptim-distribution` si ya hay publicaciones importantes.
- Mantener una copia privada y consistente de los datos del servidor, incluida su identidad de firma.
- Conservar el historial de claves públicas anteriores mientras haya revisiones firmadas con ellas.
- Después de renovar Certbot, reiniciar el servicio y confirmar que HTTPS sigue respondiendo.
- Mantener el puerto accesible sólo desde la LAN; no activar port forwarding público.
- Revisar `--check` antes de `--apply`; actualizar sólo desde `main`.

## Referencias

- [`README.md`](../README.md)
- [`docs/ADMIN_UI_LAN.md`](ADMIN_UI_LAN.md)
- [`docs/RELEASE_SIGNER.md`](RELEASE_SIGNER.md)
- [`docs/PROFILE_PROTOCOL.md`](PROFILE_PROTOCOL.md)
- [`docs/INSTALL_LINUX.md`](INSTALL_LINUX.md)
