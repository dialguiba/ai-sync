# Declaraciones de plugins portables

`.ai/plugins.yaml` acepta las entradas booleanas de Claude existentes y entradas estructuradas identificadas por un nombre lógico. El nombre lógico agrupa las variantes de paquete; no tiene que coincidir con el `name` de `plugin.json` ni con la identidad de marketplace de Claude.

```yaml
marketplaces:
  tools:
    repo: example/tools
plugins:
  old-plugin@tools: true
  toolkit:
    enabled: true
    targets:
      claude:
        install: toolkit@tools
      codex:
        path: .ai/packages/toolkit
      kiro:
        path: .ai/packages/toolkit
  remote-tool:
    enabled: false
    targets:
      codex:
        git: https://example.com/remote-tool.git
        ref: 0123456789012345678901234567890123456789
        subdir: plugin
```

`enabled` y `targets` son obligatorios en una entrada estructurada. Cada destino necesita `path`, `git` o, solo para Claude, `install`. `path` es relativo al proyecto y debe estar dentro de `.ai/`. `git` es una URL HTTPS con un `ref` de 40 dígitos hexadecimales que identifica un commit; `subdir` es opcional y no puede escapar del repositorio. Se puede declarar `install` junto con un origen de paquete local para conservar la instalación de Claude y permitir la inspección. Dos entradas no pueden reclamar la misma identidad de instalación de Claude.

El comando `ai-sync inventory <project-dir>` inspecciona los paquetes locales sin ejecutar comandos. Para Codex y Kiro busca el `plugin.json` de Agent Plugins en la raíz, skills en `skills/<nombre>/SKILL.md` y servidores en `mcp.json`. Para un paquete exclusivo de Claude reconoce `.claude-plugin/plugin.json` y `.mcp.json`, y explica por qué no se puede usar como paquete Agent Plugins en Codex o Kiro. Una fuente Git y una instalación de marketplace sin copia local quedan `unverified`: sus listas de skills y MCP permanecen vacías. No se descargan durante el inventario.

Cada entrada estructurada produce un solo recurso `plugin` en `resources`. Conserva `type`, `name`, `sources`, `destinations` y `enabled` del contrato anterior y añade `variants`, en el orden Claude, Codex, Kiro. Cada variante tiene `target`, `status`, `source`, `skills`, `mcp_servers`, `unsupported` y `requirements`; `manifest`, `package_name` y `version` aparecen cuando se pueden verificar. Los estados son `compatible`, `partial`, `incompatible`, `pending`, `unverified` e `invalid`. Una ruta local ausente queda `unverified` con la ruta exacta en `requirements`, para que se pueda copiar la declaración sin instalarla. Un paquete presente pero inválido produce un diagnóstico y deja el inventario global en `invalid`; `unverified`, `pending`, `partial` e `incompatible` describen compatibilidad sin convertir la declaración en YAML inválido.

El inventario atribuye los archivos del paquete a su recurso plugin, así que no reaparecen en `additional_files`. Los nombres de skills y MCP se muestran dentro de la variante propietaria, sin copiarlos como recursos canónicos independientes. Una coincidencia de nombre MCP entre plugins no se considera colisión porque Kiro los identifica con espacio de nombres. Dos plugins lógicos que reclaman el mismo `package_name` para el mismo destino sí producen un diagnóstico de colisión. El inventario no instala, registra ni ejecuta el contenido inspeccionado.

La sincronización y `ai-sync plugins` conservan la instalación actual de Claude para entradas booleanas y para `targets.claude.install`. Los destinos Codex y Kiro todavía no registran ni instalan estas variantes. La futura previsualización e instalación deberán usar los estados, requisitos y componentes omitidos de `variants` y mantener la instalación como una acción explícita.

Referencias de formato: [Agent Plugins plugin schema](https://agent-plugins.org/schemas/1.0.0/plugin.schema.json), [Agent Plugins MCP schema](https://agent-plugins.org/schemas/1.0.0/mcp.schema.json), [Codex plugin packaging](https://developers.openai.com/plugins/build/plugins), [Kiro Powers](https://kiro.dev/docs/powers/create/).
