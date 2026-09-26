# Abrir el panel en la red local

Distribution mantiene el listener predeterminado en loopback. Para la LAN hay
dos modos explícitos: el modo HTTP heredado, que sigue siendo exclusivamente de
solo lectura, y el nuevo límite administrativo HTTPS con login local. Ninguno
acepta `0.0.0.0`, ninguno debe reenviarse desde el router a Internet y ambos
mantienen el filtro por CIDR privado.

## Modo heredado HTTP de solo lectura

El comportamiento existente se conserva para diagnósticos en una LAN de
confianza:

```bash
/usr/local/bin/bootoptim-distribution \
  --listen 192.168.1.20:8088 \
  --admin-ui-lan \
  --admin-ui-allow-cidr 192.168.1.0/24 \
  --data-dir /var/lib/bootoptim-distribution
```

Este modo no cifra ni autentica usuarios. El panel acepta sólo `GET` y `HEAD`.
No añadas endpoints mutables a esta frontera.

## Modo administrativo HTTPS

Para el panel/API administrativo autenticado usa una IP privada concreta y un
CIDR que la contenga. Distribution termina TLS directamente; no requiere Caddy
ni otro reverse proxy.

Prepara un certificado de servidor y su clave TLS. La clave TLS es distinta de
cualquier clave de firma de releases; no copies ninguna clave Ed25519 de release
al servidor. Por ejemplo, para una prueba LAN se puede crear un certificado RSA
local con SAN para la IP (instala/confía después el certificado o su CA sólo en
los dispositivos administradores):

```bash
sudo install -d -o bootoptim-distribution -g bootoptim-distribution -m 0700 /etc/bootoptim-distribution
sudo openssl req -x509 -newkey rsa:3072 -nodes -days 365 \
  -keyout /etc/bootoptim-distribution/admin-tls.key \
  -out /etc/bootoptim-distribution/admin-tls.crt \
  -subj '/CN=bootoptim-admin' \
  -addext 'subjectAltName=IP:192.168.1.20'
sudo chown bootoptim-distribution:bootoptim-distribution \
  /etc/bootoptim-distribution/admin-tls.key /etc/bootoptim-distribution/admin-tls.crt
sudo chmod 0600 /etc/bootoptim-distribution/admin-tls.key
sudo chmod 0644 /etc/bootoptim-distribution/admin-tls.crt
```

Genera el verificador PBKDF2 de la contraseña sin poner la contraseña en la
línea de comandos ni en el repositorio. Este ejemplo usa sólo Python estándar y
600000 rondas HMAC-SHA256:

```bash
sudo python3 - <<'PY' | sudo tee /etc/bootoptim-distribution/admin-password.hash >/dev/null
import base64, getpass, hashlib, secrets
password = getpass.getpass('Admin password: ').encode()
salt = secrets.token_bytes(16)
rounds = 600000
digest = hashlib.pbkdf2_hmac('sha256', password, salt, rounds, dklen=32)
b64 = lambda b: base64.urlsafe_b64encode(b).rstrip(b'=').decode()
print('$bootoptim$pbkdf2-sha256$%d$%s$%s' % (rounds, b64(salt), b64(digest)))
PY
sudo chown bootoptim-distribution:bootoptim-distribution /etc/bootoptim-distribution/admin-password.hash
sudo chmod 0600 /etc/bootoptim-distribution/admin-password.hash
```

No hay usuario predeterminado. Elige uno explícitamente al arrancar el servicio:

```bash
/usr/local/bin/bootoptim-distribution \
  --listen 192.168.1.20:8443 \
  --admin-ui-https \
  --admin-ui-allow-cidr 192.168.1.0/24 \
  --tls-cert-file /etc/bootoptim-distribution/admin-tls.crt \
  --tls-key-file /etc/bootoptim-distribution/admin-tls.key \
  --admin-username operator \
  --admin-password-hash-file /etc/bootoptim-distribution/admin-password.hash \
  --release-public-keys-file /etc/bootoptim-distribution/release-public-keys.json \
  --data-dir /var/lib/bootoptim-distribution
```

The release public key file is a JSON object from signer key IDs to 32-byte
Ed25519 public keys encoded as unpadded base64url. The signing private key stays
offline on the administrator's computer. For example:

```json
{"wachi-release-2026":"BASE64URL_PUBLIC_KEY"}
```

Without this file, profile reads and the panel still work, but signed revision
publication fails closed because no release signer is trusted. When configured,
the file lets the service verify signed revisions before publishing them. HTTPS
mode also enables the launcher read API on the same CIDR-restricted listener.
Admin routes require the login session and a CSRF token; profile reads are
available to launcher clients on the trusted LAN.

Después abre `https://192.168.1.20:8443/admin/`. Una petición sin sesión se
redirige a `/admin/login`. La cookie de sesión es `Secure`, `HttpOnly`,
`SameSite=Strict`, host-only y expira a las ocho horas; las sesiones viven sólo
en memoria y un reinicio obliga a iniciar sesión de nuevo.

Para futuros clientes de la API administrativa, `GET /admin/api/session`
devuelve el principal y el token CSRF de la sesión. Las peticiones mutables
deben enviar ese valor en `X-CSRF-Token` además de pasar el middleware de rol
admin.

## systemd y firewall

La unidad incluida conserva loopback por defecto. Para habilitar HTTPS crea un
override local de `ExecStart` con las opciones anteriores; no cambies el usuario
del servicio ni abras permisos más amplios a los archivos de credenciales.

Ejemplo de override:

```ini
[Service]
ExecStart=
ExecStart=/usr/local/bin/bootoptim-distribution --listen 192.168.1.20:8443 --admin-ui-https --admin-ui-allow-cidr 192.168.1.0/24 --tls-cert-file /etc/bootoptim-distribution/admin-tls.crt --tls-key-file /etc/bootoptim-distribution/admin-tls.key --admin-username operator --admin-password-hash-file /etc/bootoptim-distribution/admin-password.hash --release-public-keys-file /etc/bootoptim-distribution/release-public-keys.json --data-dir /var/lib/bootoptim-distribution
```

Recarga y reinicia:

```bash
sudo systemctl daemon-reload
sudo systemctl restart bootoptim-distribution
sudo systemctl status bootoptim-distribution --no-pager
```

Permite TCP/8443 únicamente desde la subred de confianza. Con UFW, ajusta las
direcciones a tu red:

```bash
sudo ufw allow from 192.168.1.0/24 to 192.168.1.20 port 8443 proto tcp
```

El proceso comprueba además la IP real del socket contra
`--admin-ui-allow-cidr` y no confía en `X-Forwarded-For`. El filtro de red no
sustituye al login: sólo es una defensa adicional.

## Límites

La primera versión permite publicar revisiones globales firmadas, consultar su
historial y servir objetos publicados. La promoción/rollback de canales y la
integración de reparación aún requieren cerrar su flujo de operador y cliente.
El servicio nunca almacena claves privadas de firma de releases, credenciales
Microsoft/Minecraft ni dependencias de identidad externas.
