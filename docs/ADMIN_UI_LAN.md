# Abrir el panel directamente en la red local

No hace falta instalar un reverse proxy. El servicio puede servir el panel
directamente por HTTP en una interfaz LAN privada concreta y restringir todas
las peticiones al CIDR configurado. El panel actual es de sólo lectura y no
solicita contraseña: cualquier dispositivo dentro de ese CIDR puede consultar
el inventario y las métricas que muestra.

Este modo no cifra el tráfico. Úsalo sólo en una red local de confianza, bloquea
el puerto en el firewall para cualquier otra red y no configures port forwarding
en el router. No lo uses en Wi-Fi público o compartido. Si el panel incorpora
operaciones de escritura, antes habrá que añadir autenticación y HTTPS.

## Activar modo LAN

Escoge la IP privada del servidor y el CIDR de la subred de confianza. Por
ejemplo, si el servidor tiene `192.168.1.20` y los equipos de confianza están
en `192.168.1.x`, usa `192.168.1.20:8088` y `192.168.1.0/24`. La IP de escucha
debe estar dentro del CIDR. Se aceptan rangos IPv4 privados y IPv6 ULA; no se
aceptan comodines como `0.0.0.0` ni listeners públicos.

Con una unidad existente, conserva su usuario, ruta del binario y demás
restricciones. Cambia sólo los argumentos de `ExecStart` para añadir el modo
LAN. Ejemplo:

```ini
ExecStart=/usr/local/bin/bootoptim-distribution --listen 192.168.1.20:8088 --admin-ui-lan --admin-ui-allow-cidr 192.168.1.0/24 --data-dir /var/lib/bootoptim-distribution
```

El directorio de datos debe ser escribible por el usuario de la unidad. Para
una unidad que corre como `bootoptim-distribution`:

```bash
sudo install -d -o bootoptim-distribution -g bootoptim-distribution -m 0700 /var/lib/bootoptim-distribution
sudo systemctl daemon-reload
sudo systemctl restart bootoptim-distribution
sudo systemctl status bootoptim-distribution --no-pager
```

Si ya tienes una unidad con otro usuario, asigna el directorio a ese usuario y
grupo en vez de cambiar la identidad del servicio. No apuntes `--data-dir` a
otra ubicación si ya contiene una base de datos o CAS que quieras conservar.

El listener normal continúa siendo loopback. `--dev-admin-ui` es para acceso
local; no se combina con `--admin-ui-lan`. El modo LAN exige explícitamente un
listener privado concreto y `--admin-ui-allow-cidr`; además comprueba la IP de
origen de cada petición y responde `403` fuera del rango. No confía en
`X-Forwarded-For`.

## Firewall y acceso

Permite TCP/8088 únicamente desde la subred de confianza. Por ejemplo, con
UFW, reemplaza la subred por la tuya:

```bash
sudo ufw allow from 192.168.1.0/24 to 192.168.1.20 port 8088 proto tcp
```

Comprueba también que no haya una regla más amplia permitiendo el puerto y que
el router no lo reenvíe hacia Internet. Fija una reserva DHCP para que la IP
privada del servidor no cambie. Desde un dispositivo dentro de la subred abre:

```text
http://192.168.1.20:8088/admin/
```

Desde fuera del CIDR, el servicio responde `403`; conexiones a otras interfaces
no llegan al listener porque éste se enlaza sólo a la IP privada configurada.

## Datos que muestra el panel

El panel consulta SQLite y presenta revisiones publicadas, SHA-256, secuencia,
herencia y conteos/tamaño de los objetos referenciados por revisiones publicadas.
La lista está limitada a las 100 revisiones más recientes; las métricas son
agregados SQL y no recorren el árbol de objetos. No lee ni inspecciona
directorios de Minecraft.

Las operaciones de carga, publicación, promoción y rollback aún no están
expuestas por HTTP. No se habilitarán hasta tener autenticación de administrador
compatible con el contrato de Pandora, verificación de firma, resolución de
herencia y compare-and-swap de canal.
