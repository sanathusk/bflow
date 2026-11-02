> 🌐 **Idioma / Language:** **Español** · [English](../en/numbers.md)

# Cifras reales de bflow en bflow

Las tareas de este repositorio se hicieron con bflow. Esta página publica lo que midió, tarea por tarea, y cómo reproducirlo.

- **Corte:** 2026-10-16
- **Versión de bflow:** `bflow v0.1.0-rc.9.0.20261016185512-0eec012a91ca`
- **Comando:** `bflow stats --json` (la tabla sigue el orden en que lo da `bflow stats`)

## Resumen

58 tareas terminadas (fase `done`). Se excluyen las abiertas y la línea "sin tarea".

| Cifra | Mediana por tarea | Total |
|---|---|---|
| Tiempo total | 33m | 5d6h |
| Tiempo de agente | 14m | 23h49m |
| Tiempo de persona | 15m | 4d5h |
| Tokens nuevos | 221.4k | 18.2M |
| Tokens releídos de caché | 4.4M | 441.1M |

- Tareas con al menos un rechazo en una gate: 10 de 58.
- Tareas con al menos una ronda de calidad: 3 de 58.

## Tabla por tarea

| Tarea | Total | Agente | Persona | Bloqueada | Nuevos | Caché | Llamadas | Rechazos por gate | Rondas |
|---|---|---|---|---|---|---|---|---|---|
| GH-5 | 4m | 2m | 2m | - | 64.1k | 4.2M | 44 | - | - |
| GH-6 | 23m | 10m | 12m | - | 280.2k | 12.2M | 139 | - | - |
| GH-7 | 41m | 24m | 16m | - | 513.5k | 13.0M | 209 | - | - |
| GH-8 | 46m | 24m | 19m | 2m | 545.7k | 15.9M | 231 | - | - |
| GH-11 | 18m | 15m | 3m | - | 161.8k | 2.4M | 78 | - | - |
| GH-12 | 2h22m | 22m | 1h57m | 2m | 396.8k | 9.3M | 186 | - | - |
| GH-13 | 33m | 19m | 14m | - | 634.1k | 12.5M | 169 | - | - |
| GH-14 | 2h21m | 37m | 1h43m | - | 523.5k | 16.1M | 219 | - | - |
| GH-15 | 1h19m | 30m | 49m | - | 458.4k | 8.5M | 251 | walkthrough 1 | - |
| GH-16 | 3h07m | 1h31m | 1h36m | - | 1.3M | 52.2M | 649 | pause 1 · walkthrough 1 | - |
| GH-17 | 27m | 9m | 18m | - | 202.1k | 3.4M | 79 | - | - |
| GH-19 | 24m | 13m | 11m | - | 477.7k | 8.3M | 199 | - | - |
| GH-20 | 5h39m | 4h33m | 1h05m | 14s | 832.8k | 21.5M | 327 | walkthrough 1 | - |
| GH-21 | 38m | 31m | 7m | - | 244.8k | 9.2M | 122 | - | - |
| GH-26 | 23m | 9m | 14m | - | 251.9k | 4.5M | 108 | - | - |
| GH-28 | 17m | 8m | 8m | - | 195.2k | 2.6M | 93 | - | 1 |
| GH-29 | 23h07m | 21m | 22h46m | - | 522.3k | 11.8M | 194 | - | - |
| GH-32 | 24m | 12m | 12m | - | 218.1k | 6.8M | 99 | - | - |
| GH-35 | 20m | 13m | 6m | - | 221.2k | 4.8M | 126 | - | - |
| GH-37 | 26m | 6m | 19m | - | 105.5k | 2.2M | 71 | - | - |
| GH-39 | 14m | 6m | 7m | - | 144.3k | 1.8M | 56 | - | - |
| GH-46 | 34m | 23m | 11m | - | 521.6k | 9.6M | 186 | - | - |
| GH-49 | 10m | 5m | 4m | - | 153.6k | 2.2M | 84 | - | - |
| GH-50 | 53m | 27m | 26m | - | 662.0k | 17.0M | 364 | pause 1 · walkthrough 2 | - |
| GH-51 | 59m | 35m | 23m | - | 589.0k | 16.5M | 298 | - | - |
| GH-52 | 33m | 15m | 18m | - | 162.4k | 1.8M | 53 | - | - |
| GH-53 | 51m | 15m | 36m | - | 182.7k | 2.3M | 66 | - | - |
| GH-54 | 24m | 5m | 18m | - | 154.4k | 1.7M | 53 | - | - |
| GH-55 | 23m | 14m | 8m | - | 169.5k | 3.1M | 62 | - | - |
| GH-58 | 1h18m | 22m | 56m | - | 315.5k | 6.3M | 147 | - | 1 |
| GH-59 | 13m | 9m | 3m | - | 221.5k | 3.5M | 125 | walkthrough 1 | - |
| GH-60 | 24m | 14m | 10m | - | 236.9k | 3.8M | 127 | - | - |
| GH-61 | 44h31m | 41m | 43h42m | 7m | 719.1k | 13.8M | 278 | walkthrough 1 | - |
| GH-81 | 3h29m | 11m | 3h18m | - | 261.3k | 9.7M | 130 | - | - |
| GH-84 | 1h37m | 10m | 1h26m | - | 203.7k | 1.9M | 61 | - | - |
| GH-86 | 1h40m | 44m | 56m | - | 506.7k | 11.3M | 168 | - | - |
| GH-87 | 4h06m | 9m | 3h57m | - | 143.7k | 5.6M | 102 | - | - |
| GH-88 | 3h52m | 17m | 3h34m | - | 248.0k | 7.3M | 183 | walkthrough 1 | 1 |
| GH-91 | 14m | 6m | 8m | - | 86.8k | 1.2M | 47 | - | - |
| GH-94 | 2h57m | 18m | 2h39m | - | 393.6k | 22.7M | 200 | walkthrough 1 | - |
| GH-96 | 57m | 34m | 23m | - | 412.0k | 7.7M | 154 | - | - |
| GH-98 | 1h37m | 55m | 41m | - | 534.7k | 14.2M | 204 | - | - |
| GH-99 | 56m | 17m | 39m | - | 217.8k | 3.2M | 82 | - | - |
| GH-100 | 1h04m | 46m | 18m | - | 126.2k | 1.5M | 55 | - | - |
| GH-101 | 12m | 6m | 5m | - | 90.4k | 1.3M | 51 | - | - |
| GH-103 | 14m | 8m | 6m | - | 173.2k | 2.6M | 82 | walkthrough 1 | - |
| GH-108 | 30m | 17m | 10m | 2m | 253.1k | 4.1M | 103 | - | - |
| GH-118 | 7m | 4m | 3m | - | 77.6k | 1.1M | 43 | - | - |
| GH-119 | 20m | 9m | 11m | - | 147.0k | 2.6M | 65 | - | - |
| GH-120 | 13m | 7m | 6m | - | 140.2k | 1.9M | 56 | - | - |
| GH-121 | 43m | 32m | 11m | - | 238.1k | 4.7M | 98 | - | - |
| GH-122 | 16m | 12m | 4m | - | 148.0k | 2.2M | 74 | walkthrough 1 | - |
| GH-128 | 12m | 8m | 4m | - | 117.5k | 1.6M | 52 | - | - |
| GH-129 | 1h47m | 9m | 1h38m | - | 165.6k | 1.9M | 56 | - | - |
| GH-133 | 24m | 15m | 8m | - | 214.6k | 2.4M | 70 | - | - |
| GH-134 | 13m | 9m | 3m | - | 145.2k | 1.9M | 61 | - | - |
| GH-138 | 19m | 11m | 8m | - | 198.0k | 4.4M | 132 | - | - |
| LOCAL-1 | 2h06m | 1h27m | 25m | 13m | 634.3k | 17.5M | 225 | - | - |

## Notas

- **Total:** tiempo de reloj desde que la tarea empieza hasta que termina; es agente + persona + bloqueada.
- **Agente:** tiempo en que un agente trabajaba.
- **Persona:** tiempo de reloj con la tarea esperando en una gate. Incluye noches y fines de semana, por eso hay tareas de días con minutos de agente.
- **Bloqueada:** tiempo en estado bloqueado.
- **Nuevos:** input + output + escritura de caché. **Caché:** tokens releídos de caché, que se cuentan aparte porque sumarlos a lo nuevo exagera el costo.
- **Rechazos por gate** y **Rondas** salen de los eventos de la tarea. "-" significa que no hubo (o que el dato no existe), nunca que se midió cero.
- **Mediana:** unas pocas tareas largas (esperas de varios días) dominan el total; la mediana describe la tarea típica. Las medianas y totales se calcularon con los valores sin redondear de `bflow stats --json`; las celdas de la tabla están redondeadas, así que recalcular a mano puede diferir en la última cifra.
- **No hay comparación con/sin bflow.** Nadie hizo estas mismas tareas sin bflow; estas cifras describen cómo trabaja bflow, no cuánto ahorra frente a otra forma de trabajar.
- Los datos crudos (el log de cada tarea) no se versionan; esta tabla es la copia publicada del corte.

## Medir el propio repo

- `bflow stats`: una línea por tarea.
- `bflow stats <ID>`: el detalle por fase y por agente.
- `bflow stats --json`: los datos de esta tabla.
- `bflow stats --calls`: el gasto llamada por llamada.

Más en [Medir](guia.md#medir).
