# Internal Packages

This directory is the internal packages for Foreman's subsystems.

Packages currently here:

- [`internal/adapter`](adapter/)
- [`internal/api`](api/)
- [`internal/config`](config/)
- [`internal/controlplane`](controlplane/)
- [`internal/coordinator`](coordinator/)
- [`internal/core`](core/)
- [`internal/eventbus`](eventbus/)
- [`internal/identity`](identity/)
- [`internal/mcphub`](mcphub/)
- [`internal/plugin`](plugin/)
- [`internal/plugins/slack`](plugins/slack/)
- [`internal/plugins/discord`](plugins/discord/)
- [`internal/policy`](policy/)
- [`internal/sandbox`](sandbox/)
- [`internal/schemas`](schemas/)
- [`internal/statestore`](statestore/)

The code in this directory is authoritative, i.e. the only copy of the
code. You can directly modify such code.

## Using packages from Foreman code

Foreman code uses the packages in this directory directly within the module.
For example, when Foreman code imports a package from `internal/statestore`,
that import is resolved relative to the module root:

```go
// internal/core/builder.go
package core

import (
  "github.com/foreman/foreman/internal/statestore" // resolves to internal/statestore/
)
```

## Creating a new package

1. Create the package directory under `internal/`.
2. Implement the types and functions.
3. Add tests with `_test.go` suffix.
4. Wire the package into `internal/core/builder.go` if it is a top-level
   subsystem.
