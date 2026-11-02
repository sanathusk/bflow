# Contribuir a bflow

Gracias por el interés. bflow está en `v0.x` y el diseño todavía cambia; antes de un cambio grande abre un issue para acordarlo.

## Cómo ayudar

bflow es una v0.1 que el autor usa a diario y mantiene en su tiempo libre. Crece más rápido con la ayuda de quien lo usa, y cualquier aporte suma:

- **Pruébalo y cuenta qué falla.** Un issue con la plantilla de bug ya es una contribución. Qué datos incluir: [cómo probar y reportar](docs/es/como-probar-y-reportar.md).
- **Toma un issue abierto.** Los de [`good first issue`](https://github.com/innobytestech/bflow/labels/good%20first%20issue) son para empezar; los de [`help wanted`](https://github.com/innobytestech/bflow/labels/help%20wanted) (por ejemplo, los adaptadores de Linear y Codex) no están en el plan del autor y esperan a alguien que los necesite. Comenta en el issue antes de empezar para no duplicar trabajo.
- **Propón ideas.** Abre un issue con la plantilla de propuesta y lo platicamos antes de escribir código.

**Ritmo:** los issues y PRs se revisan una vez por semana. Un PR chico, con su prueba y una descripción de cómo se probó, se revisa más rápido. Las vulnerabilidades van por el reporte privado ([SECURITY.md](SECURITY.md)).

## Preparar el entorno

Requisitos: Go 1.25 o superior y git.

```bash
git clone https://github.com/innobytestech/bflow
cd bflow
go vet ./...
go test ./...        # incluye E2E que compilan el binario y usan git real
```

## Reglas del código

- **Cada cambio con su prueba.** Cada cambio llega con su prueba; un bug, con la prueba que lo reproduce.
- **Núcleo neutral.** `internal/...` nunca importa `internal/adapters/...` (lo verifica `internal/archtest`). Solo `cmd/bflow` conecta ambos.
- **Sin dependencias nuevas** fuera de la biblioteca estándar, `gopkg.in/yaml.v3` y `github.com/zalando/go-keyring`, salvo acuerdo previo en un issue.
- **Adaptadores externos** se prueban con `httptest` y respuestas grabadas, nunca contra servicios reales. Un tracker nuevo debe pasar la suite de conformidad.
- **Salida para agentes mínima.** El JSON de `--json` es compacto y sin campos redundantes; cada byte cuesta tokens.
- `gofmt` y `go vet` limpios. CI corre en Linux (con `-race`) y Windows.

## Commits y pull requests

Participar en este proyecto implica aceptar el [código de conducta](CODE_OF_CONDUCT.md).

- Mensajes estilo Conventional Commits: `feat(tracker): …`, `fix(guard): …`, `docs: …`.
- Un PR por tema, con descripción de qué cambia y cómo se probó.
- No incluyas datos internos de tu organización (URLs, IDs de proyectos, correos) en código, pruebas ni fixtures.

## Documentación bilingüe

El español es la fuente de verdad; el inglés la sigue en el mismo PR. Los archivos viven en carpetas por idioma:

| Español | English |
|---|---|
| `docs/es/cifras.md` | `docs/en/numbers.md` |
| `docs/es/comandos.md` | `docs/en/commands.md` |
| `docs/es/como-probar-y-reportar.md` | `docs/en/how-to-test-and-report.md` |
| `docs/es/conceptos.md` | `docs/en/concepts.md` |
| `docs/es/configuracion.md` | `docs/en/configuration.md` |
| `docs/es/guia.md` | `docs/en/guide.md` |
| `docs/es/instalacion.md` | `docs/en/installation.md` |
| `docs/es/por-que-y-rumbo.md` | `docs/en/why-and-roadmap.md` |
| `README.md` | `README.en.md` |
| `adapters/claude/README.md` | `adapters/claude/README.en.md` |

Reglas:

- Cada archivo abre con el banner de idioma que enlaza a su par (primera línea en `docs/`, tras el H1 en los README).
- Los enlaces internos se quedan dentro del idioma: un archivo en `docs/es/` solo enlaza archivos de `docs/es/` (más su par en `../en/` en el banner); uno en `docs/en/`, solo `docs/en/` (más su par en `../es/`). Los archivos sin traducir (`CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`) se enlazan tal cual.
- Los anchors cambian con la traducción (`#qué-garantiza-y-qué-no` ≠ `#what-it-guarantees-and-what-it-does-not`): verifica que cada `#anchor` exista en el archivo destino.
- No renombres un slug sin actualizar la tabla de arriba, todos los enlaces y el script de verificación.
- CI lo verifica con `go run ./scripts/docs-i18n`.

## Publicar una versión

1. En `CHANGELOG.md`, renombra «Sin publicar» a `## vX.Y.Z (AAAA-MM-DD)` y abre una sección «Sin publicar» vacía encima. Esa sección son las notas de la release: sin ella el workflow falla.
2. Commit, push y tag: `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. El workflow `release` espera la aprobación de un mantenedor (environment `release`: **Review deployments → Approve** en la corrida). Después corre las pruebas, compila para Windows, Linux y macOS (amd64 y arm64), firma los `.exe` si el repo tiene los secretos `WINDOWS_CERT_PFX_BASE64` y `WINDOWS_CERT_PASSWORD`, empaqueta, publica `checksums.txt` y una atestación de procedencia, y crea la release. Un tag con guion (`v0.2.0-rc.1`) sale como pre-release.

Para probar el empaquetado en local: `go run ./scripts/dist build v0.0.0 && go run ./scripts/dist package v0.0.0` (queda en `dist/`, que git ignora).

## Licencia

Al contribuir aceptas que tu aporte se publique bajo la [licencia Apache 2.0](LICENSE).

## Seguridad

Las vulnerabilidades se reportan en privado; ver [SECURITY.md](SECURITY.md).
