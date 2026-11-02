> 🌐 **Idioma / Language:** **Español** · [English](../en/guide.md)

# Cómo trabajar con bflow

Esta guía es para quien ya instaló bflow y configuró un repo (ver el [README](../../README.md) y la [instalación](instalacion.md)). Para los conceptos (fases, carriles, `next`, garantías) ve a [conceptos](conceptos.md); para `bflow.yaml` y los trackers, a [configuración](configuracion.md); para cada comando, a [comandos](comandos.md). Explica qué haces tú y qué hace bflow en cada paso, cómo ajustar a los agentes y qué hacer cuando algo se atora.

## Quién hace qué

| bflow (determinista) | El agente (criterio) | Tú (decisiones) |
|---|---|---|
| Estado de la tarea, fases y transiciones | Escribe la spec y el contrato | Eliges el carril |
| Crea la rama, abre el PR, cierra la tarea al mergear | Implementa, prueba y commitea | Apruebas o pides cambios en cada gate |
| Mueve la tarea en el tracker y comenta los rechazos | Revisa calidad, seguridad y UX | Resuelves las decisiones que el agente no puede tomar |
| Corre el check y bloquea lo peligroso | Documenta y prepara el walkthrough | Recorres el diff antes del PR |

Si aún no lo hiciste, corre `bflow install claude` dentro del repo: deja la skill y los hooks. En Claude Code le dices a la sesión algo como "sigamos con API-12" o "qué sigue". La skill `bflow` corre `bflow status`, lee `next` y hace lo que indica: te pregunta, lanza agentes o espera. No tienes que recordar comandos; cada respuesta de bflow dice el siguiente paso.

## Las gates: qué ves y qué decides

| Gate | Carril | Cuándo | Qué te muestra | Qué decides |
|---|---|---|---|---|
| Carril | Todos (fija) | Al empezar, si no pasaste `--lane` | Las opciones full, light y hotfix | Qué tan grande es el cambio |
| Discovery | `full` (fija) | Antes de la spec | La sesión te pregunta alcance, datos, errores y restricciones, hasta 3 cosas por tanda | Cuándo ya no queda ambigüedad |
| Spec | `full` y `light` (fija) | Spec lista | El brief (≤35 líneas) y, si el repo tiene UI, el UI blueprint | Aprobar o pedir cambios puntuales. Las decisiones nuevas `[N]` se ratifican una por una |
| Contrato T1 | `full` (condicional) | Solo si bflow no pudo validar el contrato solo (ver abajo) | `contract.md` | Aprobar, ajustar o volver a la spec. Al aprobar, esas pruebas quedan congeladas |
| Decisión | Todos (condicional) | Un agente no puede decidir solo | El problema y 2-3 opciones | Una opción o tu propia decisión |
| Pausa | Todos (condicional) | Implementación con el check en verde, solo si corriste `bflow pause <ID>` | — | Lanzar la revisión de calidad o volver a implementar |
| Rondas | Todos (condicional) | Calidad rechazó dos veces seguidas | — | Dividir la feature, volver a la spec o una ronda más |
| Presupuesto | Todos (condicional) | Solo con `flow.budget` en `bflow.yaml`, cuando una fase lo pasa | El gasto de la fase contra el presupuesto | Seguir o bloquear la tarea |
| Repro | `hotfix` (condicional) | El implementer reportó `DONE --no-repro "<motivo>"` | El motivo | Aceptar sin prueba de reproducción o pedirla |
| Preguntas de producto | Todos (fija) | Todo revisado y documentado | Hasta 4 preguntas sobre el comportamiento, sin decirte qué hace el código | Las contestas con tus palabras (o las saltas) |
| Walkthrough | Todos (fija) | Después de tus respuestas | Tus respuestas junto al review-map: 🔴 decisiones, 🟡 lógica, 🟢 mecánico | Aprobar y abrir el PR, volver a implementar o volver a la spec |

Las cifras de gates fijas por carril (full 5, light 4, hotfix 3) y las condiciones de cada condicional están en [conceptos](conceptos.md#gates). Aquí, lo que cambia para ti en `full`: cuando bflow valida el contrato no hay gate. Si pasa, congela las pruebas y la tarea sigue a implementing; si no, rechaza con `contract_stale` (falta commit o check), `contract_broken` (algo previo a las pruebas falla), `contract_green` (las T1 no fallan) o `contract_hollow` (pruebas vacías o que se saltan siempre). Una prueba T1 cuenta como roja solo si su nombre exacto aparece en la salida (en Go, `--- FAIL: <Func>`; en otros stacks, la ruta completa del archivo). Para probar antes de la revisión de calidad, corre `bflow pause <ID>` en cualquier fase previa a quality.

Lo que necesitas para decidir llega escrito en el chat: bflow le entrega a la sesión el texto (el brief, el contrato, el review-map) y la sesión lo copia antes de preguntar. Si alguna vez te preguntan si apruebas algo que no ves, pídelo.

El walkthrough va en dos pasos que controla bflow. Primero contestas qué esperas que haga el cambio, sin ver el código. Después la sesión compara tus respuestas con lo que el código hace, señala las diferencias y recorre contigo los cambios 🔴 uno por uno. Es la forma de revisar sin leer todo el diff.

Antes de tus respuestas, el walkthrough te dice cuánto del diff abrió el reviewer: "el reviewer leyó N de M archivos del diff", los archivos que no abrió y que no son pruebas ni docs (hasta 20), y las rutas 🔴 del review-map que no están en el diff. bflow lo mide con el hook de `Read` y de Bash del reviewer; con APPROVED rechaza si quedó sin abrir algún archivo de código o de pruebas del diff (no cuentan los docs, `specs/`, los lockfiles ni los binarios; con menos de 1,500 líneas basta un `git diff` completo), si algún archivo de una viñeta 🔴 del diff quedó sin abrir, si una viñeta 🔴 no empieza con la ruta en backticks o si el review-map no trae `## Docs` o si una viñeta 🔴 no trae debajo `- caminos:` con las salidas que el reviewer razonó (`- caminos: sin ramas nuevas` si no hay); un `caminos:` vacío o de relleno (`ninguno`, `n/a`, `-`) cuenta como ausente. Una prueba que solo lee el texto del fuente y es la única de un criterio se justifica en `## Pruebas sin ejecutar`; sin eso el reviewer la rechaza. Si el hook no ve los `Read` (`bflow doctor` lo avisa), dice "cobertura del reviewer: no medida" y no se exige. El documenter, por su lado, no cierra mientras una ruta de `## Docs` no cambie en la rama ni conste en `reports/docs.md` como "sin cambio" con su motivo.

Nadie marca una tarea como terminada a mano: bflow la cierra cuando detecta el merge (`bflow panel`). Si la terminaste en otra máquina o sesión, `bflow panel` también cierra la copia local cuando el tracker ya la da por hecha o cerrada, o cuando el PR de su rama está mergeado, en cualquier fase en que haya quedado, y lo dice ("terminada fuera de esta copia").

## Ajustar a los agentes

La configuración completa de los agentes está en [configuración](configuracion.md#agentes); aquí, cómo escribir su oficio.

`bflow render` genera los agentes en `.claude/agents/bflow-<agente>.md` y, si `agent:` es una lista con `opencode` (`agent: [claude, opencode]`), también en `.opencode/agents/bflow-<agente>.md`. Para OpenCode, `bflow install opencode` instala el comando `/bflow` y `models.opencode` traduce los alias (`opus`, `sonnet`, `haiku`) a `proveedor/modelo` (en `bflow.yaml` para el equipo; en la config global, solo para ti, y va a tu `opencode.json` sin tocar lo versionado); `render` también escribe `.opencode/plugins/bflow.js` (1.x) y `.opencode/plugins/bflow2/` (2.x), el plugin que le da a OpenCode el guard y el conteo de tokens (si `bflow` no está en el PATH, el guard no actúa y el plugin avisa una vez por sesión). OpenCode no tiene nudge de subagente ni hook de inicio: lo cubren `bflow report`, `bflow check --verify` y el bloque de AGENTS.md. Commitéalos para que todo el equipo use los mismos, y no los edites: cada archivo tiene dos partes.

- **Contrato con bflow:** qué recibe el agente, qué archivos escribe, qué veredictos puede reportar y cómo. Sale del flujo y no se configura.
- **Oficio:** cómo hace su trabajo. Trae uno por defecto y tú lo complementas en `bflow.yaml`:

```yaml
agents:
  implementer:
    model: sonnet
    effort: medium
    read: [docs/architecture/, docs/testing.md]   # lo lee antes de empezar
    extra: docs/bflow/implementer.md              # instrucciones propias
  reviewer:
    read: [docs/architecture/]
  documenter: { model: haiku, effort: low }
```

Después de cambiar `bflow.yaml` o un archivo `extra`, corre `bflow render` otra vez. En CI, `bflow render --check` falla si alguien olvidó hacerlo.

Qué poner en `extra`: lo que es propio de tu repo y no está en otro documento. Por ejemplo, el estilo de commits, cómo se nombran las pruebas, las herramientas del stack que el agente debe usar o los patrones que el equipo evita. Mejor frases concretas que énfasis: "los handlers devuelven errores con `apperr.Wrap`" sirve más que "SIEMPRE maneja bien los errores".

Con `read`, el agente no carga CLAUDE.md: las reglas le llegan por esas rutas y su contexto arranca más chico. Si tus reglas solo están en CLAUDE.md, no uses `read` o pon `omit_claude_md: false`.

Para agregar un agente propio (por ejemplo, un auditor de rendimiento), súmalo a una fase y dale su oficio:

```yaml
flow:
  agents:
    quality: [reviewer, perf-auditor]
agents:
  perf-auditor:
    extra: docs/bflow/perf-auditor.md
```

bflow le antepone el contrato: en quality escribe `reports/perf-auditor.md` y reporta APPROVED o REJECTED.

En quality trabaja por defecto solo el reviewer, que revisa también seguridad, resiliencia y rendimiento. Si quieres una segunda mirada de seguridad con su propio agente, pon `flow: { security_audit: true }`: suma el `security-auditor`, que corre en paralelo con el reviewer y cuesta sus propios tokens.

Si la tarea cambia lo que consumen otros equipos, el documenter escribe un solo changelog para ellos, versionado y en el PR. Por defecto va junto a la spec; si tu repo ya tiene un lugar para eso, dilo con `flow: { consumer_changelog: "docs/consumers/{id}-{slug}.md" }`. Con `flow.public_paths` declaras qué rutas son salida pública; si el diff las toca y el changelog no cambió, bflow rechaza el DONE del documenter (`changelog_required`). Si de verdad no hace falta, el documenter reporta BLOCKED con el motivo y tú lo eximes con `bflow unblock <ID> --no-changelog --note "motivo"`.

Los comentarios que agrega un agente explican el por qué que el código no deja ver (un invariante, una trampa, una restricción externa). No narran la tarea, la spec ni lo que se validó: quien revisa tiende a creer la intención del comentario en lugar de leer el código, y los agentes de la siguiente ronda lo toman como verdad. Lo que deba quedar escrito va en `reports/impl.md` y en el PR. bflow lo hace cumplir: ver `narrative_comment` en «Cuando algo se atora».

## Skills: cuáles convienen y cuáles chocan

bflow no depende de lo que tengas instalado: el estado, las gates y las reglas viven en el CLI y ninguna skill las salta. Pero una skill puede confundir al agente si le pide hacer a mano lo que ya hace bflow.

- **Conviene:** skills de oficio, como convenciones del stack, patrones de pruebas, arquitectura o design system. Describe su uso por el dominio ("al escribir handlers HTTP en Go"), no por el proceso.
- **Choca:** skills de proceso, como planear features, crear ramas, abrir PRs, mover tickets o aprobar specs. Quítalas de los repos que usan bflow.

`bflow doctor` avisa de las skills que parecen de proceso. Es una heurística; si una no choca, agrégala a `doctor.ignore_skills` en `bflow.yaml`.

Tus reglas de arquitectura en CLAUDE.md o en los documentos de `read` son bienvenidas: son el oficio. Si una contradice el flujo (por ejemplo, "haz push directo a main"), `guard` la bloquea y el agente recibe el motivo.

## Gastar menos contexto

- **Lo que se carga al iniciar se paga en cada llamada.** CLAUDE.md (con sus `@imports`), las reglas sin `paths:` y el índice de la memoria automática (`MEMORY.md`) viajan en cada llamada de la sesión principal. `bflow doctor` estima cuánto pesan y avisa si un archivo pasa de ~4k tokens o el total de ~10k. Poda lo que ya no aplica y deja en el índice solo enlaces de una línea. La memoria se corta a 200 líneas o 25 KB, así que lo que pase de ahí no se carga.
- **La conversación se puede descartar; el hilo vive en bflow.** El estado, las decisiones y los reportes están en `.bflow/`, la spec y el tracker. Después de aprobar una gate puedes hacer `/clear`: al reiniciar, bflow le vuelve a dar a la sesión la tarea y el siguiente paso.
- **No pegues specs ni código en el chat.** Los agentes leen por ruta y por sección (`bflow show <ID> spec --section design`).
- **Agentes con modelo y esfuerzo a su medida:** los mecánicos (documenter) con un modelo menor y esfuerzo bajo.
- `bflow stats <ID>` muestra los tokens por fase, por agente (la sesión principal aparte) y por modelo, con lo nuevo separado de la caché leída, que cuesta cerca del 10% de la entrada. Úsalo para ver si un cambio de configuración ahorró de verdad. `bflow stats <ID> --calls` agrega una tabla por llamada del modelo, una por corrida (agente o sesión principal); el encabezado de cada una es "agente · fase" (por ejemplo `implementer · contract`, con `#2` si se repite), con el contexto de cada llamada y los acumulados. Cómo leerla: lo releído en la llamada N es el contexto de la llamada N-1, porque cada llamada vuelve a leer lo anterior de la caché; por eso el acumulado de releído crece mucho más rápido que lo nuevo. El panel web muestra una tabla en Tokens, una fila por agente con sus corridas debajo; el panel abre cada corrida en una ventana con la tabla por llamada. Las tareas registradas antes del detalle por llamada avisan que no lo tienen. La tabla "por agente" de `bflow stats <ID>` suma dos columnas: `escrita` (caché escrita total del agente) y `prefijo` (lo que escribió en la primera llamada de cada corrida, en promedio; "-" si no hay detalle por llamada). Si `prefijo` es parecido en todas las corridas de un agente, el prefijo (instrucciones y herramientas) es estable y la caché se reusa; si cambia mucho, algo del inicio varía entre tareas. La caché dura unos minutos: una corrida que arranca pasado ese tiempo la vuelve a escribir.

## Cuando algo se atora

| Situación | Qué pasa | Qué haces |
|---|---|---|
| Un comando sale con código 2 | El flujo no permite eso ahora (por ejemplo, aprobar algo que no está pendiente) | Lee el motivo; repetir el mismo comando dará el mismo rechazo |
| `guard` bloqueó una acción | El agente intentó algo peligroso o que le toca a bflow | Nada: el agente recibe el motivo y qué hacer en su lugar. Un subagente que intenta `approve`, `reject`, `unblock`, `start` o `new` recibe que la decisión es de una persona y debe terminar con `bflow report` |
| OpenCode no hace nada o el guard no actúa | El plugin no cargó, o el proveedor falló (regiones Global, certificado autofirmado) y el agente quedó quieto | Corre `bflow doctor`: avisa en `log`, `proveedor` y `modelos` lo que encuentra (con el rechazo de región, qué agentes y modelos quedan bloqueados y cómo cambiarlos) en el log de OpenCode. Los casos y su arreglo están en `adapters/opencode/README.md`, sección «Problemas comunes» |
| DONE rechazado con `check_required` | No hay un check verde del commit actual | El agente corre `bflow check` y commitea |
| DONE rechazado con `repro_test_missing` (hotfix) | No hay un commit de solo pruebas, anterior a la corrección, cuyo check falló en `check.test_step`; el motivo dice cuál de los cuatro casos es | El agente hace commit de la prueba, corre `bflow check` (rojo) y luego la corrección; si el commit de prueba no tiene check, `git switch --detach <sha> && bflow check <ID> && git switch -`. Si no se puede probar, `report DONE --no-repro "<motivo>"` y tú decides en el gate `repro` |
| DONE rechazado con `narrative_comment` | Un comentario agregado en el diff cita el ID de la tarea, `R<n>`, `T<n>`, la spec, «según», «decisión» o el walkthrough | El agente lo quita o lo reescribe como un por qué, commitea y reporta DONE otra vez. `bflow check --verify` falla igual (`not_verified`). Si el patrón no aplica a tu repo (un YAML con `# T1`, una «spec de OpenAPI»), ajusta `flow.comment_patterns` o apaga `flow.narrative_comments` |
| DONE rechazado con `frozen_changed` | Cambió una prueba congelada al aprobar el contrato | Si fue un error, se deja como estaba. Si la cambiaste tú a propósito, corre `bflow freeze` desde tu terminal (un agente no puede) |
| El implementer pide cambiar una prueba congelada (NEEDS_DECISION) | La prueba del contrato está mal o quedó vieja | Si estás de acuerdo, corre `bflow freeze --allow <archivo>` desde tu terminal: el implementer la puede cambiar una vez y se vuelve a congelar cuando reporta DONE |
| El gate `freeze_allow` aparece tras aprobar esa opción | El `next` retiene al implementer hasta que corras `bflow freeze --allow <archivo>`: el guard no deja que lo corra un agente | Corre el comando en tu terminal y elige "Ya lo corrí" (o `bflow status`); si no puedes ahora, "Dejarlo para después" bloquea la tarea |
| Una decisión pide que lance otro agente o verifique a mano (el implementer no puede hacerlo) | La opción sería re-lanzar al mismo agente sin poder cumplirla | Elige la opción "La hace la sesión principal" (`bflow approve <ID> --gate decision --manual --note "<qué hacer>"`): la tarea queda bloqueada. Haz el trabajo y corre `bflow unblock <ID> --note "<resultado>"`; el agente lo ve en sus args
| El check marca `vulns (preexistente)` | Vulnerabilidades en dependencias que la tarea no tocó | Decides tú: aceptarlas en el `accept_file` del paso con fecha de revisión y abrir otra tarea, o que el implementer actualice la dependencia en esta. El `accept_file` solo lo edita una persona |
| La tarea quedó bloqueada: "terminó 3 veces sin reportar" | Un agente cortó su trabajo varias veces sin reportar a bflow | Revisa qué hizo; `bflow unblock <ID> [--note "<resultado>"]` lo relanza; la nota le llega al agente en sus args |
| Cambias de modelo o de herramienta a mitad de la feature | El estado y las gates siguen en bflow | En otra herramienta, pídele que corra `bflow status --json` y siga `next`. Sin hooks pierdes `guard` y el conteo de tokens, pero no el check ni las pruebas congeladas |
| El tracker no respondió | El cambio queda pendiente y se reintenta en orden | `bflow sync` o el siguiente comando lo reintenta |

## Medir

Todos los comandos de métricas están en [comandos](comandos.md#métricas); el reparto de tokens entre tareas se explica más abajo.

Un ejemplo publicado, con las tareas de bflow en su propio repo: [cifras](cifras.md).

Mientras trabajas, `bflow watch --open` abre en otra ventana un panel en vivo: el paso actual (quién trabaja o qué gate te espera), lo que sigue, tiempos, tokens por agente y los últimos eventos. Si configuraste `flow.budget` en `bflow.yaml`, el panel muestra "presupuesto: <gasto> / <presupuesto> nuevos" por fase, y lo marca en ámbar si pasó el límite. En `flow.loop_repeats` > 0, cuando un subagente repite la misma acción, el panel avisa "ciclo: <herramienta> repetida N veces". Para que `bflow start` lo abra solo, pon `ui: { watch: true }` en tu config global (`%AppData%\bflow\config.yaml` en Windows, `~/.config/bflow\config.yaml` en Linux y macOS) o en `bflow.yaml`. El primer `bflow init` en tu máquina te lo pregunta y lo guarda en la config global.

Cada llamada al modelo se carga a la tarea que su sesión conducía en ese momento: si corriste `bflow <ID>` o iniciaste un subagente `bflow-*` para esa tarea, sus tokens van allí. Si la sesión no condujo ninguna tarea (por ejemplo, contestaste preguntas en el chat sin comando bflow), los tokens van a una línea `sin tarea` visible en el panel. Si muchas llamadas caen en `sin tarea`, quizá haya un problema de configuración del guard o del agente.

Si prefieres el navegador, `bflow ui` abre lo mismo en una página local. En ella está la línea del carril: cada fase es una estación y el tren está en la actual. La página cabe en una sola pantalla (desde 1180x720) y muestra en un sidebar a la izquierda todos los repos donde usaste bflow, ordenados por urgencia (los que te esperan, primero), así que si trabajas en varios ves en cuál te toca. Cada fila tiene "Quitar" para sacar un repo de la vista (se avisa con "Deshacer" durante 6 segundos); los quitados quedan en "Ocultos (n)", donde "Mostrar" los regresa, y el contador se pone ámbar si uno de ellos te espera. Ocultar no des-registra el repo ni apaga sus avisos. El sidebar se puede colapsar a solo el carril; en ventanas de menos de 1180px es un panel que se abre con el botón "Repos". Con `ui: { web: true }` en la config global, la ventana del panel la abre sola al empezar una tarea, y solo una vez aunque tengas varios paneles abiertos.

`bflow stats <ID>` separa el tiempo de cada fase en trabajo del agente, espera tuya y bloqueo, y cuenta:

- **Calidad:** rechazos por gate, rondas de calidad y hotfixes ligados (`bflow start <ID> --lane hotfix --fixes <feature>`).
- **Fricción:** pedidos que el flujo rechazó, bloqueos de `guard` y agentes que terminaron sin reportar. Si sube en un repo, algo del entorno está confundiendo a los agentes: revisa sus skills y reglas.
- **Revisión:** una línea `revisión:` con la última cobertura del reviewer ("el reviewer leyó N de M archivos del diff", o "no medida"). En `--json` está en `data.stats.review`. Si la tarea no tiene cobertura, la salida no cambia.
- **Costo:** tokens nuevos y de caché por fase, por agente y por modelo.

Sin ID, `bflow stats` muestra el resumen global de todas las tareas. Si hay llamadas sin asignar a tarea alguna (por ejemplo, porque la sesión solo contestó preguntas en el chat), aparece una línea `sin tarea` con su resumen. Úsala para detectar sesiones que no conducen tareas o cambios de contexto inesperados.

Si muchas llamadas caen en `sin tarea`:

- Verifica que la sesión del agente corrió `bflow <ID>` al empezar la tarea.
- Revisa la configuración del guard: debe permitir comandos bflow.
- Verifica que el prompt del subagente contenga el ID de la tarea en formato JSON (`"id":"<ID>"`).

Antes de quitar un paso del flujo para ahorrar tokens, compara varias features: si las métricas de calidad no empeoran, ese paso sobraba.
