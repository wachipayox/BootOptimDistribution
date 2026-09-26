# BootOptim Distribution Service

Servicio privado de distribución de perfiles de **Wachiland Elite**. Es la
parte de servidor del futuro sistema de perfiles persistentes de Pandora:
publicará revisiones inmutables firmadas y objetos identificados por SHA-256.
No inspecciona archivos de jugadores ni participa en la ruta `Start` del
launcher.

El proceso Linux mantiene loopback por defecto y expone healthcheck e identidad
de versión. En el modo administrativo HTTPS, también sirve el panel privado y
la API de perfiles globales sobre el contrato definido en
`docs/PROFILE_PROTOCOL.md`. La firma privada permanece en la máquina del
operador; el servidor recibe sólo las claves públicas de confianza.

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

El panel permite preparar y publicar revisiones globales firmadas, consultar su
historial y revisar métricas del CAS. El listener predeterminado permanece en
loopback.

El modo heredado `--admin-ui-lan` conserva acceso HTTP sin login únicamente para
lectura en una LAN de confianza. Para la frontera administrativa segura usa
`--admin-ui-https`: Distribution sirve TLS directamente, exige login local,
sesión segura y CSRF, y conserva el bind privado y el filtro CIDR como defensa
adicional. Certificado, clave TLS, nombre de administrador y archivo de
verificador de contraseña son explícitos; no existe una identidad admin
predeterminada. `--release-public-keys-file` configura los verificadores
Ed25519 confiables; sin ellos, el servicio rechaza toda publicación firmada.

Consulta `docs/ADMIN_UI_LAN.md` para la configuración HTTPS, credenciales
locales, firewall y ejemplo de `systemd`. No uses `0.0.0.0`, no permitas el
puerto desde redes invitadas y no lo reenvíes desde el router a Internet. Las
claves Ed25519 privadas de firma de releases permanecen fuera del servicio.
