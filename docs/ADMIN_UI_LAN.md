# Abrir el panel en la red local

El servicio BootOptim escucha sólo en `127.0.0.1:8088`. Caddy publica el panel
en la LAN, termina HTTPS y exige autenticación HTTP Basic. No configures el
servicio para escuchar en `0.0.0.0`; tampoco reenvíes el puerto del proxy desde
el router a Internet.

## Preparar Caddy

Instala Caddy en el servidor y copia `deploy/Caddyfile.lan.example` a su
directorio de configuración. El ejemplo requiere estas variables en el entorno
del servicio Caddy:

- `BOOTOPTIM_LAN_HOST`: IP LAN estática del servidor o nombre DNS local que
  usarás en el navegador.
- `BOOTOPTIM_LAN_BIND`: la IP LAN en la que Caddy acepta conexiones, por
  ejemplo `192.168.1.20`.
- `BOOTOPTIM_ADMIN_USER`: nombre de usuario administrativo.
- `BOOTOPTIM_ADMIN_PASSWORD_HASH`: hash bcrypt generado con `caddy hash-password`.

El proceso Caddy atiende `https://<BOOTOPTIM_LAN_HOST>:8443` y reenvía al
servicio por loopback. El firewall del servidor debe permitir TCP/8443 sólo
desde la subred local de confianza. No permitas que clientes invitados o
dispositivos no confiables alcancen ese puerto.

## Certificado HTTPS interno

`tls internal` usa la CA local de Caddy. En el servidor, ejecuta `caddy trust`
para confiar en ella localmente. Para abrir el panel desde otro PC, exporta la
CA raíz que Caddy creó en su directorio de datos y añádela al almacén de
autoridades raíz de ese PC. Hazlo sólo en dispositivos de confianza y protege
el archivo de CA; la clave privada de la CA no se debe copiar. Después abre
`https://<BOOTOPTIM_LAN_HOST>:8443/admin/` y autentícate con el usuario y la
contraseña que configuraste.

Si la IP del servidor cambia, fija una reserva DHCP o actualiza las variables y
el firewall. El certificado debe cubrir exactamente la IP/nombre usado en el
navegador.

## Datos que muestra el panel

El panel consulta SQLite y presenta revisiones publicadas, SHA-256, secuencia,
herencia y conteos/tamaño de los objetos referenciados por revisiones publicadas.
La lista está limitada a las 100 revisiones más recientes; las métricas son
agregados SQL y no recorren el árbol de objetos. No lee ni inspecciona
directorios de Minecraft.

Las operaciones de carga, publicación, promoción y rollback aún no están
expuestas por HTTP. No se habilitarán hasta tener autenticación de administrador
compatible con el contrato de Pandora, verificación de firma, resolución de
herencia y compare-and-swap de canal. El panel no debe interpretarse como
autorización de esos flujos.
