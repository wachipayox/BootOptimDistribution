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

El instalador construye el binario localmente, lo coloca en
`/opt/bootoptim-distribution/bin/`, y anota el commit instalado en
`.installed-commit`. No instala aún una unidad de sistema ni abre un puerto
público.

## Prueba manual

En una terminal, arranca el proceso limitado al propio servidor:

```bash
/opt/bootoptim-distribution/bin/bootoptim-distribution --listen 127.0.0.1:8088
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
`bin/bootoptim-distribution.previous`.

No hay polling, GitHub no se conecta a tu red doméstica y las ramas que no sean
`main` nunca se instalan mediante este mecanismo.

## Próximo paso

Cuando la API de revisiones firmadas esté implementada, se añadirá una unidad
`systemd` sin privilegios, directorios de datos bajo `/var/lib`, configuración
bajo `/etc`, y el reverse proxy/autenticación que sea necesario. No expongas
el listener loopback actual a Internet.
