# Guía de uso y mantenimiento de BootOptim Distribution

Guía práctica para administrar el servicio privado de distribución de perfiles de Wachiland Elite. Está pensada para el servidor actual y distingue sus rutas conocidas de las instrucciones generales. No contiene contraseñas, claves privadas ni tokens.

**Última revisión:** 28 de septiembre de 2026

**Servidor conocido:** `wachilandserver` · Ubuntu/Debian · IP LAN `192.168.1.69`

**Versión que respondió durante la puesta en marcha:** `0.2.1` (`ce2cf5125b35ce9a8d4ddd56431ccdcb2ab9b0c1`). Comprueba siempre la versión actual con los comandos de esta guía.

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

El servidor **no** contiene la clave privada que firma las publicaciones. La firma se hace en un PC administrador con `bootoptim-release-signer`. El servidor conserva sólo las claves públicas necesarias para verificar. El servicio tampoco actualiza automáticamente desde GitHub: el administrador ejecuta manualmente el actualizador desde Linux, que sólo instala `origin/main`.

La interfaz administrativa actual está en `https://welite.ddns.net:8444/admin/`. En la LAN, el nombre `welite.ddns.net` debe resolver a `192.168.1.69` para que el certificado Certbot coincida. No uses la URL por IP como dirección habitual porque el certificado es para el nombre DNS.

El proceso escucha en `192.168.1.69:8444`, restringe clientes a `192.168.1.0/24` y usa HTTPS nativo. No necesita Caddy ni un proxy Nginx. No reenvíes este puerto desde Internet.

## Rutas y accesos actuales

| Elemento | Ruta o valor conocido |
|---|---|
| Checkout Linux | `/home/wachi/launcher_manager` |
| Binario que ejecuta systemd | `/home/wachi/launcher_manager/bin/bootoptim-distribution` |
| Otro binario instalado por el instalador | `/usr/local/bin/bootoptim-distribution` |
| Unidad systemd | `/etc/systemd/system/bootoptim-distribution.service` |
| Datos publicados y objetos | `/var/lib/bootoptim-distribution` |
| Mapa de claves públicas de firma | `/etc/bootoptim-distribution/release-public-keys.json` |
| Verificador de contraseña admin | `/etc/bootoptim-distribution/admin-password.hash` |
| Certificado TLS Certbot | `/etc/letsencrypt/live/welite.ddns.net/fullchain.pem` |
| Clave privada TLS Certbot | `/etc/letsencrypt/live/welite.ddns.net/privkey.pem` |
| Usuario/grupo del servicio | `wachi` / `wachi` |
| Bind / puerto / CIDR LAN | `192.168.1.69:8444` / `192.168.1.0/24` |
| Panel | `https://welite.ddns.net:8444/admin/` |

Las rutas de Certbot son gestionadas por Certbot y Nginx; no cambies sus permisos ni copies los archivos a mano. La unidad usa credenciales de systemd para entregarlas al proceso sin abrir los directorios privados de Certbot.

El repositorio instalado actualiza el binario local `bin/` que usa la unidad y también instala `/usr/local/bin/bootoptim-distribution`. El instalador conserva copias `.previous` de ambos binarios. El directorio de datos, `/etc/bootoptim-distribution` y el certificado son independientes del checkout y no se borran al actualizar el código.

## Uso del panel y publicación

1. Abre `https://welite.ddns.net:8444/admin/` desde la LAN e inicia sesión con el usuario administrador configurado (`wachi`) y la contraseña elegida al crear el verificador. No hay una contraseña predeterminada.
2. En Perfiles globales, crea o selecciona el perfil y prepara el contenido/revisión. Para una hija, comprueba la versión base y los permisos heredados antes de publicar.
3. Revisa la vista previa de archivos y reglas. El panel sube los objetos y ofrece descargar la solicitud de firma. Si la descarga del navegador no funciona, usa «Mostrar solicitud», copia el JSON completo y guárdalo como `profile.signing-request.json`.
4. En el PC administrador, firma localmente la solicitud con `bootoptim-release-signer`; confirma el ID exacto del perfil que muestra el programa.
5. Sube el sobre firmado al mismo flujo del panel y publica la revisión. Comprueba el historial del perfil.

Una solicitud subida sin sobre válido no publica una revisión visible. Las revisiones ya publicadas son inmutables; una corrección se publica como una secuencia posterior. La promoción o rollback de canal cambia qué revisión señala el canal, no reescribe la historia.

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

Es Ed25519 y autoriza revisiones del modpack. Debe existir sólo en el PC operador y sus copias cifradas. La clave pública correspondiente está en `release-public-keys.json` y en la lista de claves de firma confiables del launcher.

Estado conocido del PC operador:

- ID de clave activa: `wachiland-release-2026-09`.
- Archivo privado: `%LOCALAPPDATA%\WachilandLauncher\release-signing\wachiland-release-2026-09.private.json`.
- La ACL de Windows se limitó a la cuenta `WACHI-PC\Wachii`, `SYSTEM` y administradores.
- En el servidor sólo se conserva la clave pública en `/etc/bootoptim-distribution/release-public-keys.json`.

La ACL limita quién puede leer el archivo en ese PC, pero no sustituye una copia de seguridad cifrada y desconectada. No subas el archivo privado a Git, al servidor, al panel, a una carpeta compartida sin cifrar ni a una incidencia/chat.

### 2. Verificador de contraseña del panel

Está en `/etc/bootoptim-distribution/admin-password.hash`. Es un hash PBKDF2, no la contraseña recuperable. Sirve sólo para el login del panel; no firma perfiles. La unidad ejecuta el servicio como `wachi`, así que el archivo debe poder leerlo ese usuario y no otros usuarios ordinarios.

### 3. Clave privada TLS del sitio

Es la clave de Certbot `privkey.pem` para HTTPS. No firma perfiles y no es la contraseña del panel. systemd la carga con `LoadCredential=` al iniciar el proceso. No alteres los permisos de `/etc/letsencrypt` para intentar conceder acceso directo a `wachi`.

## Copia, recuperación y rotación de la clave de firma

### Preparar una copia de recuperación

Guarda una copia cifrada del archivo privado activo en un dispositivo o almacenamiento seguro que no dependa del PC operador. Guarda también el ID de clave y conserva su clave pública. Verifica que la copia cifrada se puede abrir, pero no pegues ni imprimas su contenido para comprobarla. Si usas un gestor de contraseñas o volumen cifrado, limita el acceso y documenta cómo recuperar ese cifrado.

No guardes la copia junto al repositorio ni en el mismo disco del PC como única copia. La clave pública no es secreta; la privada sí.

### Caso A: el PC falla, pero conservas la clave privada o su copia

1. Instala/abre un checkout confiable de BootOptimDistribution en el nuevo PC y localiza `cmd/bootoptim-release-signer`.
2. Restaura el archivo privado desde la copia cifrada a una carpeta privada del usuario; vuelve a restringir la ACL de Windows.
3. Usa el mismo `--key-id` y ese archivo para firmar. No hace falta cambiar la clave pública del servidor ni la confianza del launcher.
4. Continúa el perfil existente con su siguiente número de secuencia/revisión. No crees otro perfil sólo por cambiar de PC.

Comando de firma, adaptando rutas:

```powershell
go run ./cmd/bootoptim-release-signer sign `
  --key "$env:LOCALAPPDATA\WachilandLauncher\release-signing\wachiland-release-2026-09.private.json" `
  --request "$env:USERPROFILE\Downloads\profile.signing-request.json" `
  --out "$env:TEMP\profile.signed-envelope.json"
```

El firmador valida la solicitud canónica y pide escribir el ID exacto del perfil antes de firmar. No reutilices una solicitud antigua si el panel preparó una revisión nueva.

### Caso B: se perdió el PC y también todas las copias privadas

No hay forma de recuperar la clave privada a partir de la pública ni del hash de una firma. El servidor **no** puede regenerarla. Crea otra clave en un PC nuevo y autoriza explícitamente su pública en el servidor y el launcher:

```powershell
go run ./cmd/bootoptim-release-signer keygen `
  --key-id wachiland-release-2026-10 `
  --private-out "$env:LOCALAPPDATA\WachilandLauncher\release-signing\wachiland-release-2026-10.private.json" `
  --public-out "$env:TEMP\wachiland-release-2026-10-public.json"
```

1. Protege la nueva clave privada y crea inmediatamente una copia cifrada de recuperación.
2. Abre el JSON público temporal y añade **sólo su nueva entrada** al mapa existente en `/etc/bootoptim-distribution/release-public-keys.json`. No borres las claves antiguas. Mantén JSON válido y conserva una copia del archivo anterior.
3. Añade esa misma clave pública a las claves de firma confiables del launcher y distribuye una versión del launcher que la reconozca antes de entregar perfiles firmados con ella.
4. Reinicia Distribution y verifica `/v1/meta/version` y el login del panel.
5. Publica la secuencia siguiente del perfil existente con el nuevo `--key-id`. Los IDs de perfil y su historia permanecen iguales.

La ubicación exacta de la lista confiable del launcher depende de la versión privada del cliente; debe modificarse en su configuración/código de confianza de firmantes, no en una ruta inventada del servidor. Si todos los clientes antiguos sólo confían en las claves anteriores, necesitarán actualizar esa confianza antes de aceptar nuevas firmas. Conserva las claves públicas antiguas para validar revisiones históricas.

El mapa del servidor es un objeto JSON con IDs y claves públicas base64url sin padding, por ejemplo:

```json
{
  "wachiland-release-2026-09": "CLAVE_PUBLICA_EXISTENTE",
  "wachiland-release-2026-10": "NUEVA_CLAVE_PUBLICA"
}
```

No copies texto privado al servidor. La rotación no cambia el TLS ni la contraseña administrativa.

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
| Cliente rechaza una firma | Comprueba que la clave pública del `key_id` esté tanto en el mapa del servidor como en la lista confiable del launcher, y que se haya actualizado el cliente. |

No publiques capturas con cookies, tokens CSRF, hashes de contraseña, contenido de claves privadas o solicitudes de firma que puedan contener datos del perfil.

## Actualizar y volver atrás

El actualizador sigue `origin/main`, no `agent/integration-current` ni ramas de trabajo. En el panel autenticado, abre **Servicio → Actualización del servicio**. **Buscar actualización** compara el commit ejecutado con el tip público de `main`; **Actualizar y reiniciar** instala ese tip y reinicia la unidad al acabar.

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

El script mantiene `bin/bootoptim-distribution.previous` y `/usr/local/bin/bootoptim-distribution.previous`. La unidad activa usa el binario del checkout. Para restaurar manualmente el anterior, detén el servicio, conserva primero el binario actual y copia el anterior sobre el ejecutado:

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
- Mantener la clave privada de firma y una copia cifrada de recuperación bajo control del operador.
- Conservar las claves públicas anteriores en el servidor y el launcher mientras haya revisiones firmadas con ellas.
- Después de renovar Certbot, reiniciar el servicio y confirmar que HTTPS sigue respondiendo.
- Mantener el puerto accesible sólo desde la LAN; no activar port forwarding público.
- Revisar `--check` antes de `--apply`; actualizar sólo desde `main`.

## Referencias

- [`README.md`](../README.md)
- [`docs/ADMIN_UI_LAN.md`](ADMIN_UI_LAN.md)
- [`docs/RELEASE_SIGNER.md`](RELEASE_SIGNER.md)
- [`docs/PROFILE_PROTOCOL.md`](PROFILE_PROTOCOL.md)
- [`docs/INSTALL_LINUX.md`](INSTALL_LINUX.md)
