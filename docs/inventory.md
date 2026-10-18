# Inventario JSON

Ejecuta `ai-sync inventory <project-dir>` para inspeccionar el `.ai/` de un proyecto explícito. La ruta puede ser absoluta o relativa al directorio desde el que se ejecuta el CLI. `inventory` solo lee archivos: no sincroniza, instala plugins ni ejecuta comandos de `setup.yaml`.

La salida estándar contiene un documento JSON terminado en salto de línea. `resources` incluye instrucciones compartidas y por agente, reglas, agents, skills canónicas, servidores MCP, plugins declarados, eventos de hooks y entradas de setup. Cada skill agrupa `SKILL.md` y sus archivos auxiliares en `sources`; `destinations` enumera las rutas generadas actualmente. Los archivos asociados a recursos no se repiten en `additional_files`. Los demás archivos de `.ai/` figuran allí hasta que se incorporen sus tipos de recurso. Las rutas de `sources`, `destinations`, `additional_files` y `diagnostics.path` son relativas al proyecto y usan `/`.

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
    },
    {
      "type": "mcp_server",
      "name": "playwright",
      "sources": [".ai/mcp.yaml"],
      "destinations": [".codex/config.toml", ".kiro/settings/mcp.json", ".mcp.json"]
    },
    {
      "type": "plugin",
      "name": "formatter@tools",
      "sources": [".ai/plugins.yaml"],
      "destinations": [".claude/settings.json"],
      "enabled": false
    }
  ],
  "plugin_configuration": {
    "marketplaces": {
      "tools": {"source": "github", "repo": "example/tools"}
    },
    "shared_env_names": ["TOOLS_TOKEN"]
  },
  "additional_files": [],
  "diagnostics": []
}
```

Cada regla aparece una vez como `path_rule`, con el nombre tomado del archivo sin `.md`. Sus destinos incluyen las reglas de Claude y Kiro y los archivos `AGENTS.md` que Codex genera para los globs de `paths`. Cada agent aparece una vez y apunta a su destino de Claude. Una regla mal formada deja `status` en `invalid`; la regla permanece en `additional_files` y los demás recursos legibles siguen visibles.

Cada skill aparece como un recurso con `SKILL.md` y archivos auxiliares. Sus destinos incluyen Claude, Codex, Kiro Skills y Kiro Powers. Para el inventario, `SKILL.md` debe tener frontmatter YAML con `name` y `description` no vacíos. Esta validación es más estricta que la copia de archivos del comando de sincronización. Los recursos de tipo `skill` llevan `status: "ok"` o `status: "invalid"`; una skill sin `SKILL.md` sigue visible con estado inválido, junto con los demás recursos legibles.

Los servidores MCP se ordenan por nombre y se transforman a los tres destinos. Los plugins se ordenan por identidad; `enabled` refleja exactamente el valor declarado, incluso `false`. El inventario no inspecciona paquetes de plugins, no verifica su instalación ni afirma compatibilidad en otros agentes. `plugin_configuration.marketplaces` conserva la fuente y ubicación de cada marketplace en la configuración compartida; `shared_env_names` enumera las claves de `env` en orden, sin revelar sus valores, que pueden contener secretos. Estos datos no son recursos independientes ni se atribuyen a un plugin individual. Si falta `plugins.yaml`, ambos campos contienen colecciones vacías.

Un evento de hooks tiene como destino `.claude/settings.json`. Una entrada de setup tiene `destinations: []`: sus comandos se usan durante la sincronización, pero no generan un archivo de destino. Un archivo con entradas válidas queda representado por sus recursos y no se repite en `additional_files`. El inventario nunca ejecuta los comandos declarados.

`status` vale `ok`, `missing` o `invalid`. Si falta `.ai/`, el estado es `missing`. Una fuente requerida ausente o ilegible, un archivo canónico mal formado o una declaración inválida dejan el estado en `invalid`. Ambos estados producen un código de salida no exitoso y mantienen el documento JSON en stdout. Cada diagnóstico incluye la ruta afectada y la causa en `message`; una declaración MCP, de plugin, de marketplace, de evento de hooks o de entrada de setup inválida identifica además la unidad en `unit`. Los recursos siguen el orden instrucciones compartidas, instrucciones por agente, reglas, agents, skills, servidores MCP, plugins, eventos de hooks y entradas de setup, con nombres ordenados dentro de cada tipo. Los archivos adicionales se ordenan por ruta. Los diagnósticos se ordenan por ruta, unidad y mensaje.

Cuando una declaración YAML tiene una forma o un tipo inválido, el inventario conserva las demás declaraciones válidas del mismo archivo. Esto se aplica a servidores MCP, marketplaces, plugins, eventos de hooks y entradas de setup. El diagnóstico señala el archivo y la unidad inválida; un YAML con sintaxis rota produce un diagnóstico del archivo completo.
