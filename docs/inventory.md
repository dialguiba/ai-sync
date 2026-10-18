# Inventario JSON

Ejecuta `ai-sync inventory <project-dir>` para inspeccionar el `.ai/` de un proyecto explícito. La ruta puede ser absoluta o relativa al directorio desde el que se ejecuta el CLI. `inventory` solo lee archivos: no sincroniza, instala plugins ni ejecuta comandos de `setup.yaml`.

La salida estándar contiene un documento JSON terminado en salto de línea. `resources` incluye las instrucciones compartidas y por agente, cada regla válida, cada agent canónico y cada carpeta de skill como un recurso `skill`. Cada skill agrupa `SKILL.md` y sus archivos auxiliares en `sources`; `destinations` enumera sus rutas generadas para Claude, Codex, Kiro Skills y Kiro Powers. Los archivos de recursos no se repiten en `additional_files`. Los demás archivos de `.ai/` figuran allí hasta que se incorporen sus tipos de recurso. Las rutas de `sources`, `destinations`, `additional_files` y `diagnostics.path` son relativas al proyecto y usan `/`.

```json
{
  "status": "ok",
  "resources": [
    {
      "type": "shared_instruction",
      "name": "project",
      "sources": [".ai/project.md"],
      "destinations": [".kiro/steering/project-conventions.md", "AGENTS.md", "CLAUDE.md"]
    },
    {
      "type": "agent_instruction",
      "name": "codex",
      "sources": [".ai/targets/codex.md"],
      "destinations": ["AGENTS.md"]
    }
  ],
  "additional_files": [".ai/setup.yaml"],
  "diagnostics": []
}
```

Cada regla aparece una vez como `path_rule`, con el nombre tomado del archivo sin `.md`. Sus destinos incluyen `.claude/rules/<name>.md`, `.kiro/steering/<name>.md` y los archivos `AGENTS.md` que Codex genera para los globs de `paths`. Un glob sin prefijo de directorio estable apunta al `AGENTS.md` raíz; varios globs que apuntan al mismo archivo producen un solo destino. Cada `.ai/agents/<name>.md` aparece una vez como `agent` y apunta a `.claude/agents/<name>.md`, su destino actual.

Una regla con YAML, lista `paths` o delimitadores de frontmatter mal formados deja `status` en `invalid`. Su diagnóstico señala la fuente y explica la causa. La regla inválida permanece en `additional_files`; las demás reglas, agents e instrucciones legibles siguen visibles en `resources`.

`status` vale `ok`, `missing` o `invalid`. Si falta `.ai/`, el estado es `missing`. Si `.ai/` existe pero falta una fuente requerida, una instrucción presente no se puede leer, una regla está mal formada, hay YAML mal formado en un archivo canónico o una skill carece de `SKILL.md` válido, el estado es `invalid`. Para el inventario, `SKILL.md` debe tener frontmatter YAML con `name` y `description` no vacíos. Esta validación del inventario es más estricta que la copia de archivos del comando de sincronización. Un archivo ilegible dentro de una skill también se diagnostica. Ambos estados producen un código de salida no exitoso y mantienen el documento JSON en stdout; cada diagnóstico incluye la ruta afectada y la causa en `message`. Los demás recursos siguen visibles aunque uno sea inválido. Los recursos siguen el orden instrucciones, reglas por nombre, agents por nombre y skills por nombre; los archivos adicionales y diagnósticos se ordenan por ruta.
