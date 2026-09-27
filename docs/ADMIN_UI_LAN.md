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

### Reutilizar el certificado Certbot de `welite.ddns.net`

En el servidor de Wachiland ya existe un certificado de Certbot. Sus rutas son:

- Certificado y cadena: `/etc/letsencrypt/live/welite.ddns.net/fullchain.pem`
- Clave privada TLS: `/etc/letsencrypt/live/welite.ddns.net/privkey.pem`

No copies estos archivos ni cambies permisos en `/etc/letsencrypt`: en este
servidor, los directorios pertenecen a `root:pterodactyl` y están cerrados a
otros usuarios. En su lugar, `systemd` puede cargar los archivos y exponerlos
sólo al proceso del servicio mediante `LoadCredential=`. Consulta la
[documentación de credenciales de systemd](https://github.com/systemd/systemd/blob/main/docs/CREDENTIALS.md#configuring-per-service-credentials).

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

El login comprueba el token CSRF enviado en el formulario contra una cookie
host-only, `Secure` y `SameSite=Strict`. Si el navegador envía `Origin: null`
por usar un origen opaco, el servicio continúa hasta esa comprobación; los
orígenes explícitamente distintos siguen rechazándose. Las mutaciones autenticadas
también requieren su token CSRF de sesión.

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

Después abre el nombre HTTPS que corresponda a tu certificado y puerto. Una petición sin sesión se
redirige a `/admin/login`. La cookie de sesión es `Secure`, `HttpOnly`,
`SameSite=Strict`, host-only y expira a las ocho horas; las sesiones viven sólo
en memoria y un reinicio obliga a iniciar sesión de nuevo.

Para futuros clientes de la API administrativa, `GET /admin/api/session`
devuelve el principal y el token CSRF de la sesión. Las peticiones mutables
deben enviar ese valor en `X-CSRF-Token` además de pasar el middleware de rol
admin.

## systemd y firewall

La unidad incluida conserva loopback por defecto. Para habilitar HTTPS crea un
override local de `ExecStart` con las opciones anteriores. No cambies el usuario
del servicio ni abras permisos más amplios a los certificados.

En el servidor de Wachiland, Nginx ya ocupa el puerto `8443`, por lo que
Distribution escucha directamente en `192.168.1.69:8444`. Aplica este override
con `sudo systemctl edit bootoptim-distribution`; conserva la contraseña hash
generada anteriormente:

```ini
[Service]
LoadCredential=admin-cert:/etc/letsencrypt/live/welite.ddns.net/fullchain.pem
LoadCredential=admin-key:/etc/letsencrypt/live/welite.ddns.net/privkey.pem
Environment=ADMIN_TLS_CERT=%d/admin-cert
Environment=ADMIN_TLS_KEY=%d/admin-key
ExecStart=
ExecStart=/home/wachi/launcher_manager/bin/bootoptim-distribution --listen 192.168.1.69:8444 --admin-ui-https --admin-ui-allow-cidr 192.168.1.0/24 --tls-cert-file=${ADMIN_TLS_CERT} --tls-key-file=${ADMIN_TLS_KEY} --admin-username wachi --admin-password-hash-file /etc/bootoptim-distribution/admin-password.hash --data-dir /var/lib/bootoptim-distribution
```

`LoadCredential` evita dar acceso a `wachi` a los directorios privados de
Certbot. systemd entrega al proceso una copia temporal de cada archivo al
arrancar el servicio. Tras una renovación del certificado, reinicia el servicio
para que cargue el certificado nuevo; no hace falta copiar archivos.

Recarga y reinicia:

```bash
sudo systemctl daemon-reload
sudo systemctl restart bootoptim-distribution
sudo systemctl status bootoptim-distribution --no-pager
```

Permite TCP/8444 únicamente desde la subred de confianza. Con UFW, ajusta las
direcciones a tu red:

```bash
sudo ufw allow from 192.168.1.0/24 to 192.168.1.69 port 8444 proto tcp
```

El proceso comprueba además la IP real del socket contra
`--admin-ui-allow-cidr` y no confía en `X-Forwarded-For`. El filtro de red no
sustituye al login: sólo es una defensa adicional. Para que el nombre coincida
con el certificado desde la LAN, abre
`https://welite.ddns.net:8444/admin/` y configura DNS local para que
`welite.ddns.net` resuelva a `192.168.1.69`. No hace falta que Nginx haga de
proxy para Distribution.

## Límites

La primera versión permite publicar revisiones globales firmadas, consultar su
historial y servir objetos publicados. El panel permite promover una revisión
al canal `stable`; la interfaz de rollback y la integración de reparación
siguen pendientes.
El servicio nunca almacena claves privadas de firma de releases, credenciales
Microsoft/Minecraft ni dependencias de identidad externas.
