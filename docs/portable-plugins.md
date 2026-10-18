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

La sincronización y `ai-sync plugins` conservan la instalación actual de Claude para entradas booleanas y para `targets.claude.install`. Los destinos Codex y Kiro no se instalan durante la sincronización: `ai-sync plugins preview` y `ai-sync plugins apply` usan los estados, requisitos y componentes omitidos de `variants` y mantienen la instalación como una acción explícita.

Referencias de formato: [Agent Plugins plugin schema](https://agent-plugins.org/schemas/1.0.0/plugin.schema.json), [Agent Plugins MCP schema](https://agent-plugins.org/schemas/1.0.0/mcp.schema.json), [Codex plugin packaging](https://developers.openai.com/plugins/build/plugins), [Kiro Powers](https://kiro.dev/docs/powers/create/).

## Previsualización por destino

`ai-sync plugins preview <project-dir> --target codex|kiro` devuelve un objeto JSON en una sola línea. La ruta del proyecto es obligatoria; el destino solo puede ser `codex` o `kiro`. No modifica archivos, instala paquetes, ejecuta comandos ni descarga fuentes Git. `ai-sync plugins` sin `preview` sigue siendo el instalador explícito de Claude. La sincronización ordinaria sigue copiando las declaraciones y conserva su comportamiento actual para Claude, sin activar plugins de Codex o Kiro.

El objeto contiene `target`, `fingerprint`, `plugins` y `diagnostics`. `plugins` está ordenado por nombre lógico y contiene solo las variantes declaradas para el destino. Cada elemento conserva `name`, `enabled`, `package` (la procedencia declarada), `manifest`, `version`, `native_identity` (`plugin.json.name`), `skills`, `mcp_servers`, `omitted_components`, `missing_requirements`, `state`, `action` y `conflicts`. Los estados de inventario se conservan: `compatible`, `partial`, `incompatible`, `pending`, `unverified` o `invalid`. `partial` significa que el paquete contiene componentes no soportados; su acción es `pending` hasta que se produzca y valide un paquete específico para el destino. Una fuente Git no se descarga: queda `unverified` y sin acción de instalación. Las variantes deshabilitadas tampoco tienen acción.

`action.kind` puede ser `command`, `manual`, `pending`, `blocked` o `none`. Una variante local compatible de Kiro muestra `command: ["kiro-cli", "powers", "install", "<ruta-absoluta-del-paquete>"]`, compatible con Kiro CLI 2.25 o posterior; en la IDE se puede usar Powers → Add Custom Power → Import power from a folder. Para Codex local, `manual` indica crear una entrada en `.agents/plugins/marketplace.json` cuya `source.path` apunte al paquete relativo a la raíz del proyecto, reiniciar Codex y ejecutar `codex plugin add <native_identity>@<marketplace-name>`. La previsualización no promete que una carpeta suelta se pueda instalar directamente con `codex plugin add`: Codex requiere un marketplace. Una colisión de `native_identity` o `package.path` entre plugins habilitados del mismo destino establece `action.kind: blocked` para ambos hasta elegir propietarios o rutas e identidades distintas. Las skills y servidores MCP permanecen dentro de su plugin; nombres repetidos entre paquetes no se consideran colisiones.

`fingerprint` es SHA-256 hexadecimal del contrato de inventario y de los nombres y bytes de todos los archivos de `.ai/`, ordenados por ruta relativa. La huella cambia al modificar contenido del paquete, declaraciones, requisitos detectados o cualquier otro archivo canónico, y evita depender de la ruta absoluta del proyecto. La previsualización de fuentes remotas no fija bytes que todavía no se han obtenido.

Mecanismos nativos: [Codex plugin packaging and marketplaces](https://developers.openai.com/plugins/build/plugins), [Codex plugin CLI](https://github.com/openai/codex/blob/main/codex-rs/cli/src/plugin_cmd.rs), [Kiro Powers installation](https://kiro.dev/docs/powers/installation/), [Kiro CLI 2.25 release](https://kiro.dev/changelog/cli/2-25/).

## Aplicación por destino

`ai-sync plugins apply <project-dir> --target codex|kiro --fingerprint <fingerprint> (--all | --plugin <nombre>)` aplica las acciones de una previsualización revisada y devuelve un objeto JSON en una sola línea con `target`, `fingerprint` y `plugins`. Cada elemento informa `name`, `state`, `status`, `error`, `command` e `instructions`. Los estados de aplicación son `installed`, `manual`, `pending`, `unverified`, `incompatible`, `disabled` y `failed`.

El comando vuelve a inspeccionar el proyecto y compara la huella con `--fingerprint`. Si `.ai/` cambió, la aplicación se rechaza sin instalar nada y hay que previsualizar de nuevo: lo que se instala es exactamente lo revisado. `--fingerprint` y la selección (`--all` o una o más veces `--plugin`) son obligatorios; los nombres lógicos sin variante para el destino se rechazan.

Una colisión real de `native_identity` o `package.path` cancela la aplicación completa con un error, sin ejecutar ningún instalador, hasta que se elija un propietario o una identidad distinta. Las variantes `incompatible` y `unverified` se informan con su motivo y no se instalan: una fuente Git sigue sin descargarse durante la aplicación. Un paquete `partial` queda `pending` porque la instalación parcial necesita un paquete específico del destino preparado y validado, que todavía no se produce; por eso no existe una aceptación que lo fuerce.

Para Kiro, una variante local `compatible` ejecuta `kiro-cli powers install <ruta-absoluta>`. Para Codex, la aplicación no ejecuta nada: devuelve como `manual` las instrucciones concretas del registro en el marketplace y el `codex plugin add`, porque la instalación ocurre dentro del agente. Ninguna variante deshabilitada se aplica, y la aplicación no instala plugins durante la sincronización ordinaria: copiar `.ai/` y ejecutar `ai-sync` siguen siendo acciones separadas de instalar plugins.
