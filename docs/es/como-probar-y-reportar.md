> 🌐 **Idioma / Language:** **Español** · [English](../en/how-to-test-and-report.md)

# Cómo probar bflow y reportar issues

Gracias por probar bflow. Va en la v0.1.0, la primera versión publicada: hay cosas que fallan, y lo más útil que puedes hacer es contarnos cuáles.

## Qué cambió desde la rc.9

- `guard` cubre más casos en Bash: opciones globales de git, redirecciones, `tee`, `rm`, `mv` y `cp` hacia `.env` y `.bflow/`, y escrituras a las pruebas congeladas (#150).
- `report DONE` rechaza con `narrative_comment` los comentarios que citan el ID, `R<n>`, `T<n>` o la spec (#151).
- El reviewer tiene un eval propio para medir si atrapa defectos (#152).
- El carril full ya no abre las gates de contrato ni de pausa por defecto: el contrato se valida con el check y solo abre gate en los casos de [conceptos](conceptos.md#gates); la pausa es opt-in con `bflow pause` (#153, #173).
- En hotfix, `report DONE` exige un commit de solo pruebas cuyo check falló, o `--no-repro` con gate `repro` (#159).

## Qué esperamos de ti

1. **Instala** ([instalación](instalacion.md)) y corre `bflow version` y `bflow doctor`.
2. **Recorre una tarea con el tracker local**, sin conectar nada, en un repo de prueba:

   ```bash
   cd mi-repo
   bflow init
   bflow install claude          # o: bflow install opencode
   bflow new --lane light --title "Una tarea pequeña"
   ```

   Después abre tu agente y dile "qué sigue". Cada respuesta de bflow dice el siguiente paso; sigue hasta el PR.
3. **Repite con tu tracker** (Plane o GitHub, ver [configuración](configuracion.md)) y con el agente que usas de verdad.
4. Apunta lo que te sorprendió: un mensaje confuso, un paso que no entendiste, algo que tuviste que preguntar.

Para entender qué esperar en cada paso, lee la [guía](guia.md) y los [conceptos](conceptos.md).

## Qué pedimos que pruebes

- **Una tarea `full` con check configurado** (con un paso `test`): confirma que no aparece la gate contract. T1 reporta `CONTRACT_READY`, bflow lo valida y la tarea sigue sola a implementing. Si apareció la gate, anota por qué te lo dijo.
- **Un hotfix con prueba roja y luego verde:** commit solo con la prueba y `bflow check` en rojo; después la corrección, `bflow check` en verde y `DONE`. Prueba también que `DONE` sin ese commit se rechaza con `repro_test_missing`.
- **`bflow pause <ID>`** en una tarea antes de quality: la gate aparece al terminar implementing.

## Una pregunta por feature cerrada

Cuando una feature termine y se mergee, contesta: **¿hubo que corregir algo después del merge que el reviewer debió atrapar?** Respóndelo en un comentario del issue de la feature o abre un issue nuevo con el título «Post-merge: <feature>». Con un "no" basta.

## Cómo reportar un issue útil

Abre un issue en <https://github.com/innobytestech/bflow/issues> con:

- La salida de `bflow version`.
- La salida de `bflow doctor`.
- Qué agente (Claude Code u OpenCode, y su versión) y qué tracker usas.
- Tu sistema operativo.
- Los pasos, en orden, hasta el problema, y qué esperabas que pasara.
- La salida completa del comando que falló. Si salió con código 2, repítelo con `--json` y pega todo: trae el motivo y el `next`.

No pegues tokens ni el contenido de `.env`. Si el problema es de una tarea, `bflow show <ID> check` y `bflow stats <ID>` ayudan.

## Problemas conocidos

Hoy no hay bugs abiertos que conozcamos: los que figuraban en la rc.9 (#39, #52, #53, #86 y #91) están cerrados. Lo que sigue pendiente son mejoras y trabajo planeado; míralo en los [issues abiertos](https://github.com/innobytestech/bflow/issues) antes de reportar.
