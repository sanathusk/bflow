> 🌐 **Idioma / Language:** **Español** · [English](../en/installation.md)

# Instalación

## Requisitos

- **Claude Code 2.1.271 o posterior**, si lo usas como agente (`bflow doctor` revisa la versión). **OpenCode**, si lo usas como agente.
- **Go 1.25 o superior**, solo para `go install` o para compilar desde el código. Los instaladores no necesitan Go.
- git. Para PR con GitHub, un token (ver [configuración](configuracion.md)).

## Instalar

Se instala una vez por máquina. Cada repo solo necesita un `bflow.yaml` corto; nunca se copia código de bflow al repo.

Linux y macOS (en `~/.local/bin`; `BFLOW_INSTALL_DIR` cambia la carpeta):

```bash
curl -fsSL https://raw.githubusercontent.com/innobytestech/bflow/main/install.sh | sh
```

Windows (en `%LOCALAPPDATA%\Programs\bflow`, que agrega a tu `PATH`; `BFLOW_INSTALL_DIR` cambia la carpeta):

```powershell
irm https://raw.githubusercontent.com/innobytestech/bflow/main/install.ps1 | iex
```

En Windows, `BFLOW_INSTALL_NO_PATH=1` evita que el script toque tu `PATH`.

### Fijar una versión

Los scripts instalan la última versión estable. `BFLOW_VERSION` fija una versión, con o sin la `v`:

```bash
curl -fsSL https://raw.githubusercontent.com/innobytestech/bflow/main/install.sh | BFLOW_VERSION=v0.1.0 sh
```

```powershell
$env:BFLOW_VERSION = 'v0.1.0'; irm https://raw.githubusercontent.com/innobytestech/bflow/main/install.ps1 | iex
```

### Verificar lo que bajaste

Los dos scripts verifican el SHA-256 del archivo contra `checksums.txt` antes de instalar. También puedes bajar el archivo de tu plataforma de [Releases](https://github.com/innobytestech/bflow/releases). Cada release trae una atestación de procedencia:

```bash
gh attestation verify <archivo> --repo innobytestech/bflow
```

Comprueba que lo compiló el workflow del repo.

### Con Go o desde el código

```bash
go install innobytes.tech/bflow/cmd/bflow@latest
```

```bash
git clone https://github.com/innobytestech/bflow.git
cd bflow
go install ./cmd/bflow
```

## Comprobar la instalación

```bash
bflow version    # imprime la versión
bflow doctor     # valida config, herramientas, conexiones, entorno y hooks
```

Si `bflow` no se encuentra, la carpeta de instalación no está en tu `PATH`.

## Instalar en tu herramienta de agente

Dentro del repo:

```bash
bflow install claude      # o: bflow install opencode
```

- **Claude Code:** instala la skill en `~/.claude/skills/bflow/SKILL.md` (una vez por máquina), fusiona los hooks, el permiso y la barra de estado en `<repo>/.claude/settings.json` sin tocar lo tuyo, y corre `bflow render` si el repo tiene `agent: claude`.
- **OpenCode:** deja el comando `/bflow` en `$XDG_CONFIG_HOME/opencode/commands/bflow.md` (o `~/.config/opencode/commands/bflow.md`); con `opencode` en `agent:` también corre `render`, que escribe los agentes y el plugin en `.opencode/`.
- Es idempotente: córrelo otra vez cuando quieras. `--skill-only` instala solo la skill o el comando.

Los detalles (hooks, plugin, modelos de OpenCode) están en [configuración](configuracion.md#claude-code-y-opencode).

## Actualizar

```bash
bflow update            # actualiza a la última versión publicada
bflow update --check    # solo dice si hay una versión nueva
bflow update --force    # reemplaza aunque este binario no venga de una versión publicada (por ejemplo, uno compilado por ti)
```

`bflow update` elige el canal según lo que tienes instalado: con una prerelease recibes la siguiente (por ejemplo, de `v0.1.0-rc.10` a `v0.1.0`); con una estable nunca recibes prereleases. También refresca la skill de Claude Code y el comando de OpenCode si ya estaban instalados.

## Desinstalar

No hay un comando `uninstall`. A mano:

1. Borra el binario `bflow` (la carpeta de instalación de arriba).
2. Borra la skill: `~/.claude/skills/bflow` y, si usas OpenCode, `bflow.md` en la carpeta `commands` de OpenCode.
3. En cada repo, quita las entradas de bflow de `.claude/settings.json` (hooks, permiso y barra de estado).
4. Borra lo que escribió `bflow render`: los archivos `bflow-*.md` de `.claude/agents/` y `.opencode/agents/`, `.opencode/plugins/bflow.js`, `.opencode/plugins/bflow2/` y el bloque de bflow en `AGENTS.md`.
5. Borra `bflow.yaml` y la carpeta `.bflow/` del repo, y la config global (`~/.config/bflow/`, en Windows `%AppData%\bflow\`) si ya no la quieres.
6. Quita las credenciales del llavero del sistema (Credential Manager, Keychain o Secret Service): las que guardó `bflow connect`.
