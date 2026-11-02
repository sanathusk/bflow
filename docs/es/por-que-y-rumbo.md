> 🌐 **Idioma / Language:** **Español** · [English](../en/why-and-roadmap.md)

# Por qué existe bflow y hacia dónde va

Soy Alfonso, de [Innobytes](https://innobytes.tech). Hice bflow para mi equipo y para el lugar donde trabajo, y ahora quiero que otros lo prueben.

## Mi historia

Trabajo con IA desde hace casi un año, y con un *harness* (prompts, subagentes y scripts que guían al agente) para resolver features desde hace 6 o 7 meses. Sé que la IA mejora cada vez más. No quiero inventar nada ni meterme con los modelos: quiero guiarlos para que resuelvan features con spec driven development (SDD).

Empecé con un harness por proyecto. Uno de ellos lo usaba mucho y lo modificaba todo el tiempo, y los de los demás proyectos se quedaban desactualizados. Cada vez que volvía a otro proyecto tenía que actualizar su harness a mano, y eso me quitaba tiempo. Además:

- Las features tardaban mucho en resolverse.
- Había alucinaciones.
- Los agentes a veces no seguían el harness al pie de la letra.
- Se generaba mucha documentación redundante.

No probé otras herramientas. Vi videos de gente que armaba su propio harness para guiar agentes, basados en Kiro o Spec Kit, pero nunca los instalé ni los investigué a fondo. Lo que hice fue ordenar a mi manera lo que ya me funcionaba.

## Para qué lo hice

- Gastar menos tokens, y que el contexto de cada feature pase de un agente a otro sin releer conversaciones completas.
- Seguir un proceso de desarrollo.
- Hacer más ameno el desarrollo ahora que el dev ya no programa tanto.
- Guiar a los agentes por buen camino con spec driven development.
- Que todos los devs del equipo resuelvan una feature de la misma manera, aunque la IA no sea determinista.

Quiero que otros lo prueben y, si les sirve, sigan esta metodología: SDD a mi manera, sin inventar nada nuevo, con trackers para dar seguimiento a las tareas. También quiero mostrar cómo trabajo y que son trabajos de calidad.

## Lo que medí

Estos datos salen del harness anterior a bflow, que tenía en dos repos reales, uno de backend y otro de frontend. Las cifras de bflow están en [Cifras del equipo](#cifras-del-equipo):

- **Las reglas vivían en prosa.** "OBLIGATORIO", "NUNCA", "no saltes la compuerta". El modelo las respetaba casi siempre, y justo ese "casi" es donde se cuelan los errores caros.
- **El modelo movía el estado a mano.** Cambiaba estados en el tracker, creaba ramas y armaba URLs de PR, cada vez a su manera. En un repo el flujo estaba validado por scripts; en el otro, no.
- **Gastaba tokens en trabajo mecánico.** Leer el tracker, releer bitácoras, interpretar la salida de 4 comandos de pruebas, mantener archivos de estado. En uno de los repos, unos 7.000 tokens fijos por sesión antes de hacer nada útil.
- **Llenaba el repo de estado de trabajo.** Entre el 73% y el 80% de los archivos del harness versionados eran bitácoras, reportes y estado, no especificaciones.
- **Cada repo tenía su copia.** Se desviaban: estados distintos, prefijos de rama distintos, reglas que un repo tenía y el otro no.

## Cifras del equipo

Lo que el equipo ha probado con bflow v0.1.0-rc.9 (con las gates de contrato y pausa del carril full, antes de #153), medido con `bflow stats` el 2026-10-20/21. Anónimo y sin maquillar: 51 tareas terminadas en 6 repos.

| Repo | Tipo | Dev | Tareas | Terminadas | Mediana agente | Mediana persona | Mediana tokens nuevos | Mediana caché | Con rechazo en gate | Con ronda de calidad |
|---|---|---|---|---|---|---|---|---|---|---|
| Repo A | backend | dev 1 | 31 | 28 | 49m | 49m | 668.6k | 11.6M | 14 | 2 |
| Repo B | frontend | dev 1 | 6 | 5 | 32m | 36m | 698.8k | 28.0M | 0 | 1 |
| Repo C | sitio web | dev 1 | 12 | 11 | 9m | 9m | 275.1k | 2.5M | 2 | 3 |
| Repo D | API | dev 1 | 3 | 3 | 1h40m | 25m | 991.9k | 31.5M | 1 | 0 |
| Repo E | frontend | dev 1 | 1 | 1 | 35m | 1h11m | 631.2k | 11.4M | 0 | 0 |
| Repo D | API | dev 2 | 3 | 2 | 2h45m | 67h20m | 679.4k | 10.5M | 0 | 0 |
| Repo F | punto de venta | dev 3 | 4 | 1 | 19m | 23h16m | 344.8k | 5.9M | 1 | 0 |

Los dos grupos van separados y no se suman como "el equipo":

| Grupo | Terminadas | Con rechazo en gate | Con ronda de calidad |
|---|---|---|---|
| dev 1 (el autor de bflow) | 48 | 17 | 6 |
| Otros devs (dev 2 y dev 3) | 3 | 1 | 0 |

Notas:

- **Versión:** rc.9, antes de #153.
- **Tiempo de persona:** es la espera en las gates (incluye noches y fines de semana), no tiempo trabajado.
- **Medianas:** solo por fila. No hay mediana global: la mediana de medianas no es la mediana real.
- **Muestra de otros devs:** 3 tareas terminadas; no es una tendencia.
- **Calidad tras el merge:** no se midió. Queda pendiente para v0.1.1.
- **Sin comparables:** no hay tareas equivalentes hechas sin bflow.
- **Faltan datos de 2 personas.**
- bflow construyendo bflow se mide aparte, con otro corte (2026-10-16): [Cifras](cifras.md).

## La meta

Que una persona decida lo importante (qué se construye, el contrato, qué se mergea) sin leer todo lo que genera el agente, y que el resto del proceso no dependa de la memoria del modelo.

## La idea

Separar lo que **requiere criterio** de lo que es **determinista**:

- El **agente** entiende el problema, diseña, programa y revisa.
- El **CLI** lleva el estado, las transiciones, git, el tracker, las pruebas y las reglas.

El agente no tiene que recordar el proceso: le pregunta a `bflow` qué sigue y lo hace. Un solo binario se instala una vez por máquina; cada repo solo trae un `bflow.yaml` corto. Los conceptos están en [conceptos.md](conceptos.md).

## Estado

bflow va en la **v0.1.0**, la primera versión publicada. Funciona de punta a punta con el tracker local, Plane y GitHub Issues, y con Claude Code y OpenCode. La API de comandos puede cambiar antes de la 1.0.

## Rumbo

Lo de abajo es el plan, no lo ya hecho. Cada hito enlaza su milestone en GitHub.

| Hito | Criterio de salida | Milestone |
|---|---|---|
| v0.1.1 | Mejoras que salieron de v0.1.0 para no retrasar la publicación: amarillos en el walkthrough (#154) y métricas en el PR (#74). | [v0.1.1](https://github.com/innobytestech/bflow/milestone/8) |
| Bloque 2 · Difusión | Después de publicar v0.1.0: video (#69), topics (#67), README en inglés (#62), repo de ejemplo (#76), artículo en español (#161) y en inglés (#68), comparativas (#70). | [Bloque 2](https://github.com/innobytestech/bflow/milestone/6) |
| Bloque 3 · Después de publicar | Sin orden fijo: memoria por repo (#156, se decide con datos de uso), política de estabilidad (#72) y bflow completo en inglés (#191, cuando lleguen usuarios que no hablen español). Los adaptadores de Linear (#73) y Codex (#75) están abiertos para quien quiera contribuirlos. | [Bloque 3](https://github.com/innobytestech/bflow/milestone/7) |
| 1.0 | API de comandos estable. | Sin milestone todavía |

### Módulos del producto

1. Hecho: motor de flujo (estado, gates, tracker local, Plane y GitHub Issues y Projects, git y GitHub).
2. Hecho (versión inicial): guardas (git destructivo, `.env`, `.bflow/`, pruebas congeladas incluso sin hooks, ramas y PR a mano, tamaño de diff, agentes que terminan sin reportar).
3. En curso: distribuidor. `bflow render` genera los agentes para Claude Code y OpenCode; hay binarios por plataforma con checksums y atestación, instaladores y `bflow update`. Pendiente: Codex y la firma de código en Windows y macOS (falta el certificado).
4. Hecho: métricas (tiempo, tokens por fase, agente y modelo, calidad y fricción).
5. Previsto: contexto, un índice del código para el planner y el reviewer.
6. Previsto: planeación, ordenar el backlog y repartirlo en ciclos balanceados.

Siguiente: [instalación](instalacion.md) · [cómo probar y reportar](como-probar-y-reportar.md).
