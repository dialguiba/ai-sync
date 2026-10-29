# ai-sync

Canonical agent configurations that ai-sync reads, copies and transforms for different targets.

## Language

**Catalog**:
View of the resources and additional files of a project's canonical configuration.

**One-off copy**:
Transfer of a configuration, complete or selected, from a source to a target at a given moment. It does not create a persistent link between the two.
_Avoid_: Cross-project sync

**Additional file**:
File of the canonical configuration that is preserved when copying the configuration in full, but has no identity of its own as a selectable resource.

**Plugin-provided resource**:
Resource a person can find by its type, but whose addition and removal belong to the plugin that provides it.

**Declared plugin**:
Plugin chosen for a project with intended targets and one origin per available variant. Its declaration does not imply that it is installed or that it is compatible with every target.

**Installed plugin**:
Package added to a target agent so that its resources are available there. It can be declared in a project without being installed yet.

**Plugin variant**:
Package of the same plugin prepared for a specific target agent. Its resources and requirements may differ from those of other variants.
