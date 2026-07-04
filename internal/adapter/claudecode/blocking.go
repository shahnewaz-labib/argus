package claudecode

import "github.com/MunifTanjim/argus/internal/adapter"

// ShouldBlock reports whether the event requires a user decision.
func ShouldBlock(ev adapter.HookEvent) bool { return EventName(ev) == "PermissionRequest" }
