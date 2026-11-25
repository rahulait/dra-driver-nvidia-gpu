# Legacy checkpoint fixtures

`checkpoint-v1.json` and `checkpoint-v2.json` were serialized with the pre-V3 envelope, claim, status, and prepared-device layouts from commit `9ca111cffd06cf9686b704ea5eb85771781cc2b0`, using their original checksum algorithms.
The fixtures include nested allocation status, opaque configuration, device metadata, and preparation progress supported by each version.
They are fixed compatibility inputs; do not regenerate them when changing working-state types.

`checkpoint-v2-optional-absent.json` derives from the V2 fixture with optional device, PCI, and sharing-configuration fields omitted, using the original checksum scopes.
It covers reads and rewrites of older files that lack those fields.
