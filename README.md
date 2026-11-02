# Byteflow · `bflow`

> 🌐 **Language / Idioma:** **Español** · [English](README.en.md)

**Motor de flujo Spec-Driven Development para trabajar con agentes de IA.** Un solo binario en Go que lleva cada tarea de la idea al PR mergeado (discovery, spec, implementación, revisión, walkthrough; en el carril full, también un contrato de pruebas de aceptación que se congela antes del código), decide las transiciones, habla con tu tracker y con git, y le dice al agente exactamente qué hacer después, en pocas líneas de JSON.

> Estado: **v0.1.0**, la primera versión publicada. Funciona de punta a punta con el tracker local, Plane y GitHub, y con Claude Code y OpenCode como agente. La API de comandos puede cambiar antes de la 1.0. Si lo vas a probar, empieza por [cómo probar y reportar](docs/es/como-probar-y-reportar.md).

---

## Por qué existe

Tenía un harness por proyecto y se desactualizaban entre sí; además, las features tardaban, había alucinaciones, los agentes no siempre seguían el harness y se generaba documentación redundante. En los dos repos que medí, el harness gastaba unos 7.000 tokens fijos por sesión y entre 73% y 80% de lo versionado era estado, no especificaciones. Hice bflow para mi equipo en Innobytes: gastar menos tokens y que todos resuelvan una feature igual con spec driven development, aunque la IA no sea determinista.

Byteflow separa lo que **requiere criterio** (entender el problema, diseñar, programar, revisar), que hace el agente, de lo que es **determinista** (estado, transiciones, git, tracker, pruebas, reglas), que hace el CLI. El agente no tiene que recordar el proceso: pregunta a `bflow` qué sigue y lo hace. La historia completa y el rumbo: [Por qué y rumbo](docs/es/por-que-y-rumbo.md).

## Para quién es (y para quién no)

bflow es opinado a propósito: tiene un flujo definido y gates humanas fijas. Muestra una forma concreta de trabajar con agentes.

**Te sirve si:**

- Desarrollas features con agentes de IA y quieres que las decisiones importantes (qué se construye, qué se mergea) las tome una persona, sin leer todo lo que el agente genera.
- Trabajas con git y PRs, y con Claude Code u OpenCode como agente.
- Quieres saber cuánto cuesta cada feature (tiempo, tokens, iteraciones) y dónde se atora.

**No te sirve si:**

- Buscas que el agente trabaje solo de punta a punta, sin aprobaciones.
- Tu flujo no pasa por ramas y PRs.
- Usas otra herramienta de agente y necesitas todas las garantías: hoy solo Claude Code y OpenCode tienen adaptador (ver [qué garantiza](docs/es/conceptos.md#qué-garantiza-y-qué-no)).

## Instalar

Linux y macOS (en `~/.local/bin`):

```bash
curl -fsSL https://raw.githubusercontent.com/innobytestech/bflow/main/install.sh | sh
```

Windows (en `%LOCALAPPDATA%\Programs\bflow`):

```powershell
irm https://raw.githubusercontent.com/innobytestech/bflow/main/install.ps1 | iex
```

Los scripts verifican el SHA-256 del archivo antes de instalar. Versión fija, Go, actualizar y desinstalar: [Instalación](docs/es/instalacion.md).

## Inicio rápido

```bash
cd mi-repo
bflow init                 # detecta stack, remoto, rama base y propone los pasos de check
bflow doctor               # valida config, herramientas, conexiones y hooks
bflow install claude       # o: bflow install opencode (skill o comando, y hooks)
```

Sin `bflow.yaml`, bflow funciona con el tracker local y solo con git. Para recorrer una tarea:

```bash
bflow new --lane light --title "Validación de RFC" --file idea.md   # crea la tarea y la arranca
bflow report LOCAL-1 --agent spec-author --verdict READY    # lo corre el agente
bflow show LOCAL-1 brief
bflow approve LOCAL-1                                        # crea la rama
# ... el implementer programa y corre `bflow check` ...
bflow report LOCAL-1 --agent implementer --verdict DONE     # con pasos de check, exige un check verde
bflow status
```

Cada comando imprime el siguiente paso; con `--json`, ese paso llega en `next`. En la práctica le dices a tu agente "qué sigue" y él lo corre. Para conectar Plane o GitHub: [Configuración](docs/es/configuracion.md).

## Qué garantiza

Lo que vive en el CLI se cumple con cualquier herramienta (el estado y las gates los maneja bflow, `DONE` exige un check verde, las pruebas del contrato no cambian). Lo que depende de hooks, solo donde hay hooks (Claude Code y OpenCode): bloquear `git reset --hard`, push forzado, `.env` y `.bflow/`. bflow no garantiza la calidad del código ni contiene a un agente malintencionado. La tabla completa: [Conceptos](docs/es/conceptos.md#qué-garantiza-y-qué-no).

## Qué hace bflow y qué no

Gates humanas fijas por carril: `full` tiene 5 (carril, discovery, spec, preguntas de producto y walkthrough), `light` 4 (sin discovery) y `hotfix` 3 (carril, preguntas y walkthrough). Otras gates abren solo en ciertos casos: decisión, rondas, `contract` (full, solo si bflow no puede validar el contrato), `pause` (solo con `bflow pause`) y `repro` (hotfix, solo con `--no-repro`). Condiciones y detalle: [Conceptos](docs/es/conceptos.md#gates).

- **Sí:** lleva el estado y las transiciones, crea la rama y abre el PR, exige un check verde para el `DONE`, congela las pruebas del contrato y mide tiempo, tokens y rechazos.
- **No garantiza la calidad del código:** la revisan los agentes y tú; bflow asegura que esos pasos ocurran.
- **No contiene a un agente malintencionado:** `guard` es una barandilla contra errores comunes, no un sandbox.
- **No cubre todo en Bash:** `guard` reconoce los comandos y rutas listados en [Conceptos](docs/es/conceptos.md#qué-garantiza-y-qué-no); un comando rebuscado puede pasar.

## Cifras reales

Hay dos mediciones. La del equipo (rc.9): 51 tareas terminadas en 6 repos, 48 de ellas del autor y 3 de otros devs, con calidad tras el merge todavía sin medir; la tabla y sus notas están en [Cifras del equipo](docs/es/por-que-y-rumbo.md#cifras-del-equipo). La de bflow construyendo bflow (58 tareas, corte al 2026-10-16): mediana de 33m de reloj, 14m de agente y 15m esperando a una persona; tabla por tarea en [Cifras](docs/es/cifras.md).

No hay comparación con/sin bflow, y no es una promesa de ahorro. Para medir el tuyo: `bflow stats`.

## Rumbo

Hoy: v0.1.0, la primera release anunciada. Previsto: [v0.1.1](https://github.com/innobytestech/bflow/milestone/8), mejoras que salieron de v0.1.0; [Bloque 2 · Difusión](https://github.com/innobytestech/bflow/milestone/6); [Bloque 3 · Después de publicar](https://github.com/innobytestech/bflow/milestone/7); y la 1.0 con la API de comandos estable. El criterio de cada hito: [Por qué y rumbo](docs/es/por-que-y-rumbo.md#rumbo).

## Documentación

| Documento | Para qué |
|---|---|
| [Por qué y rumbo](docs/es/por-que-y-rumbo.md) | La motivación, la meta y los hitos. |
| [Instalación](docs/es/instalacion.md) | Requisitos, instalar, actualizar, verificar, desinstalar. |
| [Conceptos](docs/es/conceptos.md) | Núcleo y adaptadores, fases y carriles, gates, `next`, qué queda en el repo, garantías. |
| [Cómo trabajar con bflow](docs/es/guia.md) | El día a día: qué haces en cada gate, ajustar agentes, qué hacer cuando algo se atora. |
| [Configuración](docs/es/configuracion.md) | `bflow.yaml`, perfiles, trackers, agentes, check, Claude Code y OpenCode. |
| [Comandos](docs/es/comandos.md) | Referencia de todos los comandos. |
| [Cifras](docs/es/cifras.md) | Tiempo, tokens, rechazos y rondas por tarea de bflow en su propio repo. |
| [Cómo probar y reportar](docs/es/como-probar-y-reportar.md) | Qué esperamos de quien prueba, cómo reportar y problemas conocidos. |

## Desarrollo

```bash
go vet ./...
go test ./...        # incluye E2E que compilan el binario y usan git real
```

- Dependencias: biblioteca estándar, `gopkg.in/yaml.v3` y `github.com/zalando/go-keyring`.
- El núcleo (`internal/...`) nunca importa `internal/adapters/...`; lo verifica `internal/archtest`. El único lugar que conecta ambos es `cmd/bflow`.
- Los adaptadores externos se prueban con `httptest` y respuestas grabadas, nunca contra servicios reales.
- Guía para contribuir y cómo ayudar: [`CONTRIBUTING.md`](CONTRIBUTING.md#cómo-ayudar). Vulnerabilidades: [`SECURITY.md`](SECURITY.md). Código de conducta: [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## Licencia

[Apache License 2.0](LICENSE). Copyright 2026 Innobytes ([innobytes.tech](https://innobytes.tech)).
