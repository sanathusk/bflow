> 🌐 **Idioma / Language:** **Español** · [English](README.en.md)

# Adaptador de Claude Code

`bflow install claude` instala la skill y fusiona los hooks; no hay que copiar nada a mano. Los agentes los escribe `bflow render` en `<repo>/.claude/agents/bflow-<agente>.md` y se commitean.

| Archivo (embebido en el binario) | Dónde lo deja `bflow install claude` | Para qué |
|---|---|---|
| `skill_head.md` + [`../leader/bflow.md`](../leader/bflow.md) | `~/.claude/skills/bflow/` (una vez por máquina) | La sesión principal interpreta `next`; no contiene reglas del flujo |
| `settings.json` | `<repo>/.claude/settings.json` (se fusiona: lo tuyo se conserva; las entradas de bflow se reemplazan) | Hooks: `bflow hook session-start`, `bflow guard --reads` antes de Bash/Edit/Write/Read (el `Read` solo se registra si es del reviewer en quality: `reads.jsonl`), `bflow guard --post` tras cada Bash (si cambió una prueba congelada, la restaura y avisa al agente), `bflow hook tokens` al terminar turnos y subagentes, `bflow hook subagent-stop` al terminar un agente `bflow-*`; barra de estado `bflow statusline` |

Los hooks van por repo y no globales: `bflow guard` aplica reglas (ramas protegidas, `.env`, pruebas congeladas) que solo tienen sentido donde hay `bflow.yaml`.

`bflow doctor` avisa si el repo declara `agent: claude` y faltan los hooks o la skill instalada no coincide con la de este bflow.
