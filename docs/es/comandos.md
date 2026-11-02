> 🌐 **Idioma / Language:** **Español** · [English](../en/commands.md)

# Comandos

`bflow help` lista todos. Cualquier comando acepta `--json` y responde con el mismo envelope (ver [`next`](conceptos.md#el-contrato-con-el-agente-next)). Códigos de salida: `0` bien, `1` error, `2` rechazo del flujo. Los corchetes son opcionales; si omites `ID`, se usa la tarea activa.

## Flujo

| Comando | Qué hace |
|---|---|
| `status [ID] [--brief]` | Estado y siguiente paso. |
| `new --lane full\|light\|hotfix --title "t" [--file idea.md] [--slug s]` | Crea una tarea y la arranca. |
| `start <ID> --lane full\|light\|hotfix [--slug s] [--fixes ID]` | Empieza una tarea en un carril; `--fixes` liga un hotfix a la feature que corrige. |
| `approve [ID] [--gate g] [--choice n] [--note t] [--file f \| --stdin]` | Aprueba el gate pendiente. Con `--gate budget`, "Sí" sigue la fase. `--stdin` (solo gate discovery) recibe el discovery completo por stdin, hasta 64 KiB; bflow lo escribe en `## Discovery` de la spec. `--file` sigue funcionando. |
| `reject [ID] --note "motivo" [--gate g] [--to fase]` | Rechaza el gate pendiente. Con `--gate budget`, "No" bloquea la tarea con la nota y no avanza la fase. En `quality` y `documenting`, sin gate pendiente, regresa la tarea a `implementing` (`--note` obligatorio). |
| `report [ID] --agent a --verdict V [--note] [--file] [--option o]... [--stdin] [--no-repro "motivo"]` | Un agente reporta su veredicto (`--stdin`: reporte del scout). `--no-repro`: solo `DONE` del implementer en hotfix; cierra sin prueba de reproducción y abre el gate humano `repro` (`approve --gate repro` acepta; `reject --gate repro --note` pide la prueba). |
| `block [ID] --reason "motivo"` | Bloquea la tarea. |
| `unblock [ID] [--note t] [--no-changelog]` | Desbloquea la tarea y vuelve a su fase. Con `--no-changelog` (exige `--note`) exime al documenter del changelog cuando el diff toca `flow.public_paths`; el motivo queda en la bitácora. Solo una persona. |
| `pause [ID]` | Pide parar antes de quality: al terminar implementing se abre la gate `pause` para que pruebes. Se puede repetir; en quality o después sale `pause_late`. Solo una persona. |
| `drop <ID> --note "motivo"` | Retira una tarea sin terminarla. |
| `freeze [ID] [--allow <archivo>]` | Vuelve a congelar las pruebas del contrato tal como están; con `--allow` deja cambiar una una vez. Solo una persona. |
| `show [ID] task\|brief\|spec\|contract\|review-map\|questions\|decisions\|discovery\|check\|scout [--section s]` | Muestra un artefacto. |
| `task add "título" [--description t]` | Crea una tarea (trackers que lo permiten, como local). |
| `task list` | Lista las tareas abiertas del tracker. |
| `sync [ID]` | Reintenta los cambios pendientes con el tracker. |
| `import --from harness [--dir ruta] [--state-only] [--dry-run]` | Importa el harness anterior una vez. |

## Agentes

| Comando | Qué hace |
|---|---|
| `render [--check]` | Genera los agentes de bflow para la herramienta del repo; con `--check` falla si no coinciden con la configuración. |
| `install claude\|opencode [--skill-only]` | Instala la skill (o el comando) y los hooks de bflow para tu herramienta. |

## Git y PR

| Comando | Qué hace |
|---|---|
| `pr [ID]` | Publica la rama y abre o actualiza el PR de una tarea en `in_review`. |
| `panel [--sla]` | Compromisos: cierra lo mergeado o terminado en el tracker, aunque esté en otra fase, sincroniza y avisa de gates vencidos. |

## Calidad

| Comando | Qué hace |
|---|---|
| `check [ID] [--quick pkg] [--verify]` | Compuerta determinista. |
| `env check [--quiet]` | Verifica API, puertos y variables del entorno local. |
| `guard` | Hook PreToolUse: lee la acción por stdin y la bloquea (exit 2) si rompe una regla. |
| `eval reviewer [--craft ref] [--case c]... [--abort] [--from-fixes]` | Mide qué defectos conocidos atrapa el reviewer. Corre un caso a la vez con el agente `bflow-eval-reviewer` y escribe `.bflow/evals/<fecha>.json` con atrapados, no atrapados, posibles falsos positivos, tokens y tiempo. Sin flags, retoma la corrida abierta. |
| `eval reviewer report <caso> --verdict APPROVED\|REJECTED [--note t]` | Lo usa el agente del eval para reportar su caso; no se corre a mano. |

`eval reviewer`:

- `--craft <ref>`: lee el oficio del reviewer de ese ref git (vacío = el árbol). Sirve para comparar oficios: corre el eval con el de cada versión.
- `--case <nombre>`: corre solo ese caso (se puede repetir).
- `--abort`: cancela la corrida abierta y quita sus worktrees y carpeta. No escribe resultado.
- `--from-fixes`: crea un esqueleto `draft: true` en `.bflow/evals/reviewer/` por cada hotfix con `start --fixes`, con `base` y `head` ya llenos cuando se puede. Completa `defects` y quita `draft` a mano.

Los casos viven en `.bflow/evals/reviewer/<caso>/case.yaml`: `base` y `head` (commits), `defects` (`file`, `lines` como `N` o `N-M`, `what`) y `spec` opcional. Un caso mal formado rechaza la corrida con `case_invalid` y sin crear nada. El puntaje cuenta un defecto como atrapado si un hallazgo del mismo archivo cae a ±5 líneas. Las carpetas `.bflow/evals/` se versionan; el resto de `.bflow/` no.

## Métricas

| Comando | Qué hace |
|---|---|
| `stats [ID] [--calls] [--reads]` | Tiempo por fase (agente, humano, bloqueada), iteraciones y tokens. Sin ID, una línea por tarea. |
| `statusline` | Una línea para la barra de estado (lee una caché: milisegundos). |
| `watch [--interval 2s] [--once] [--open] [--web]` | Panel en vivo de la tarea. |
| `ui [--port 7719] [--no-open]` | El panel en el navegador, con todos tus repos. |

`stats` con ID, además:

- `--calls`: tabla de llamadas al modelo por corrida (agente o sesión principal), con contexto nuevo y caché reutilizada en cada llamada. Sirve para ver dónde se repite contexto o si el prefijo varía entre corridas.
- `--reads`: qué archivos se leyeron y cuántas veces, cuántos agentes distintos los tocaron y qué porcentaje fueron relecturas. Sin `reads-all.jsonl`, avisa "sin registro de lecturas".

## Configuración

| Comando | Qué hace |
|---|---|
| `init [--yes] [--profile p] [--tracker local\|plane\|github] [--project P]` | Crea `bflow.yaml` detectando stack, remoto, rama base y pasos de check (con github, `--project owner/N`). |
| `doctor` | Valida config, herramientas, conexiones, entorno y hooks. |
| `connect plane\|github [--token t] [--url u] [--workspace w] [--from-gh]` | Guarda una credencial en el llavero tras validarla. |
| `profile add <nombre> [--tracker --url --workspace --host --base --agent]` | Crea o completa un perfil global. |
| `profile list` | Lista los perfiles globales. |
| `profile use <nombre>` | Usa un perfil en este repo. |
| `tracker setup [--dry-run]` | Crea en el tracker los estados que las fases necesitan. |
| `tracker states` | Estados del tracker y la fase con la que bflow los lee y escribe (solo lectura). |

## Hooks

Los instala `bflow install`; no se corren a mano.

| Comando | Qué hace |
|---|---|
| `hook session-start` | Entorno, compromisos y tarea activa en pocas líneas. |
| `hook tokens` | Hook Stop y SubagentStop: suma los tokens nuevos a la fase. En SubagentStop suma también las llamadas nuevas de la sesión principal (sin avisar ni podar marcas); el Stop avisa si la tarea espera a la persona. |
| `hook subagent-stop` | Si un agente de bflow terminó sin reportar, lo hace seguir (máximo 2 veces) y después bloquea la tarea. |

## Mantenimiento

| Comando | Qué hace |
|---|---|
| `update [--check] [--force]` | Actualiza bflow a la última versión publicada. |
| `version` | Versión de bflow. |
| `help` | Lista los comandos. |
