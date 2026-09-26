# BootOptim Distribution Service

Servicio privado de distribución de perfiles de **Wachiland Elite**. Es la
parte de servidor del futuro sistema de perfiles persistentes de Pandora:
publicará revisiones inmutables firmadas y objetos identificados por SHA-256.
No inspecciona archivos de jugadores ni participa en la ruta `Start` del
launcher.

La primera entrega deja un proceso Linux mínimo, limitado a loopback, con
healthcheck, identidad de versión y un instalador/actualizador administrado por
el propio servidor. La API de perfiles firmados se implementará sobre el
contrato ya aprobado en Pandora PR #35; no se sustituye dicho contrato por una
API improvisada.

## Actualización administrada desde Linux

El servidor **no** recibe conexiones de GitHub. Una vez clonado este repositorio
privado con una deploy key de sólo lectura, el administrador ejecuta:

```bash
sudo /opt/bootoptim-distribution/scripts/update.sh --check
sudo /opt/bootoptim-distribution/scripts/update.sh --apply
```

`--check` compara el commit instalado con `origin/main`; `--apply` sólo acepta
el tip actual de `main`, construye el binario localmente y conserva el binario
anterior hasta que la construcción termina bien. La política es que `main` es
la única rama desplegable y debe protegerse con revisión/CI en GitHub.

El instalador inicial y la unidad `systemd` se documentarán al habilitar el
primer servicio operativo. Por ahora el binario se puede ejecutar de forma
explícita para la prueba:

```bash
/opt/bootoptim-distribution/bin/bootoptim-distribution --listen 127.0.0.1:8088
curl http://127.0.0.1:8088/v1/meta/version
```

## Endpoints actuales

- `GET /healthz` — disponibilidad del proceso.
- `GET /v1/meta/version` — versión semántica, commit instalado, esquema de
  protocolo y capacidades expuestas.

Los endpoints de distribución sólo se abrirán junto con autenticación,
verificación Ed25519, manifiestos canónicos y pruebas de herencia/anti-rollback.

## Panel de administración en red local

El panel muestra perfiles y revisiones persistidas, herencia fijada y métricas
del CAS. Sigue siendo de sólo lectura mientras no estén implementadas las
operaciones HTTP de publicación/promoción con verificación de firma. Por
defecto el proceso sólo acepta loopback. Para uso directo en una LAN de
confianza, enlázalo a una IP privada concreta y permite únicamente el CIDR de
esa red:

```bash
/usr/local/bin/bootoptim-distribution --listen 192.168.1.20:8088 --admin-ui-lan --admin-ui-allow-cidr 192.168.1.0/24 --data-dir /var/lib/bootoptim-distribution
```

Consulta `docs/ADMIN_UI_LAN.md` para el firewall y la instalación. No uses
`0.0.0.0`, no permitas el puerto desde redes invitadas y no lo reenvíes desde
el router a Internet. Todo dispositivo del CIDR permitido podrá ver este panel
de sólo lectura; antes de añadir operaciones de escritura hará falta una capa
de autenticación y transporte cifrado.
