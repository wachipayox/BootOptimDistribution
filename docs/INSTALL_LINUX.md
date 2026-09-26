# Instalación y uso en Linux

Este repositorio es público, pero no contiene perfiles, mods, configuraciones,
objetos CAS, tokens ni claves de firma. Esos datos se guardarán sólo en el
servidor Linux cuando se implemente la API de distribución firmada.

## Instalación inicial

En Ubuntu/Debian, ejecuta una sola vez:

```bash
sudo apt-get update && sudo apt-get install -y git golang-go curl
sudo git clone https://github.com/wachipayox/BootOptimDistribution.git /opt/bootoptim-distribution
sudo /opt/bootoptim-distribution/scripts/install.sh
```

El instalador construye el binario localmente, lo conserva bajo `bin/` en el
checkout, publica un ejecutable root-owned en
`/usr/local/bin/bootoptim-distribution` y anota el commit instalado en
`.installed-commit`. El servicio seguirá limitado a loopback; no abre un puerto
público.

### Ruta personalizada

La instalación puede vivir fuera de `/opt`. Por ejemplo, para usar
`/home/wachi/launcher_manager`:

```bash
sudo git clone https://github.com/wachipayox/BootOptimDistribution.git /home/wachi/launcher_manager
sudo /home/wachi/launcher_manager/scripts/install.sh
```

Los scripts detectan automáticamente su propia carpeta, así que después de
clonar en esa ruta no hace falta conservar ni repetir una variable de entorno:

```bash
sudo /home/wachi/launcher_manager/scripts/update.sh --check
sudo /home/wachi/launcher_manager/scripts/update.sh --apply
```

## Prueba manual

En una terminal, arranca el proceso limitado al propio servidor:

```bash
/usr/local/bin/bootoptim-distribution --listen 127.0.0.1:8088
```

En otra terminal:

```bash
curl -i http://127.0.0.1:8088/healthz
curl http://127.0.0.1:8088/v1/meta/version
```

La respuesta de versión expone la versión semántica, el commit y las
capacidades activas. No contiene datos de jugadores ni secretos.

## Buscar e instalar una actualización

El servidor consulta GitHub sólo cuando el administrador lo solicita:

```bash
sudo /opt/bootoptim-distribution/scripts/update.sh --check
sudo /opt/bootoptim-distribution/scripts/update.sh --apply
```

`--check` compara `.installed-commit` con `origin/main` y muestra ambas
versiones. `--apply` descarga exclusivamente el tip de `main`, compila antes
de sustituir el binario y conserva el binario previo como
`bin/bootoptim-distribution.previous` y
`/usr/local/bin/bootoptim-distribution.previous`.

No hay polling, GitHub no se conecta a tu red doméstica y las ramas que no sean
`main` nunca se instalan mediante este mecanismo.

## Panel LAN

El ejemplo de unidad `systemd` y el modo directo de acceso LAN están en
`deploy/` y `docs/ADMIN_UI_LAN.md`. El modo LAN enlaza una IP privada concreta
y filtra por CIDR, pero no cifra ni autentica usuarios; úsalo sólo en una red de
confianza y nunca reenvíes el puerto desde el router. El panel es de solo
lectura: todavía no publica revisiones ni promueve canales.
