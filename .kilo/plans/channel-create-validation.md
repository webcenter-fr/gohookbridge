# Plan: Channel Creation — Validation + Description + Rename project→channel

## Objective
Three-fold:
1. **Rename** `project` → `channel` everywhere (types, routes, FSM commands, BoltDB keys, permissions, roles, JSON fields)
2. **Add `description`** optional field to channels
3. **Validate** channel ID (no spaces, no special chars) on backend and frontend, using `go-playground/validator/v10` on all handlers
4. **Fix** the "New Channel" modal in the UI — it currently does nothing

---

## ⚠️ Breaking Changes
- **BoltDB keys**: `/projects/` → `/channels/` — data migration needed or fresh deploy
- **FSM commands**: `create-project` → `create-channel` — Raft log incompatibility
- **API routes**: `/api/projects` → `/api/channels` — frontend and external clients
- **JSON fields**: `projects` → `channels` in User and Binding payloads
- **Permissions**: `project:read` → `channel:read`, etc.
- **Roles**: `project_admin` → `channel_admin`, `project_viewer` → `channel_viewer`

---

## Phase 1 — Rename `project` → `channel` (Go backend)

### 1.1 `gohookbridge/store/types.go`
| Before | After |
|--------|-------|
| `type Project struct` | `type Channel struct` |
| `DefaultProjectConfig` | `DefaultChannelConfig` |
| `resolveProjectConfig()` | `resolveChannelConfig()` |
| `User.Projects []string` | `User.Channels []string` |
| `UserBinding.Projects []string` | `UserBinding.Channels []string` |
| `PermProjectRead` | `PermChannelRead` |
| `PermProjectWrite` | `PermChannelWrite` |
| `PermProjectView` | `PermChannelView` |
| `"project:read"` | `"channel:read"` |
| `"project:write"` | `"channel:write"` |
| `"project:view"` | `"channel:view"` |
| `{Name: "project_admin"}` | `{Name: "channel_admin"}` |
| `{Name: "project_viewer"}` | `{Name: "channel_viewer"}` |

JSON tags:
- `json:"projects"` → `json:"channels"`

### 1.2 `gohookbridge/store/api.go`
| Before | After |
|--------|-------|
| `/projects` route prefix | `/channels` |
| `contextKeyProjectID` | `contextKeyChannelID` |
| `projectCtx()` | `channelCtx()` |
| `listProjects()` | `listChannels()` |
| `createProject()` | `createChannel()` |
| `getProject()` | `getChannel()` |
| `updateProject()` | `updateChannel()` |
| `deleteProject()` | `deleteChannel()` |
| `PermProjectRead` | `PermChannelRead` |
| `PermProjectWrite` | `PermChannelWrite` |
| `UserProjects()` | `UserChannels()` |
| `hasProjectAccess()` | `hasChannelAccess()` |
| `allowedProjects` | `allowedChannels` |
| `Project` type | `Channel` type |
| error messages (`"project"`) | → `"channel"` |
| `writeCSV` header `"id,name"` + filename `projects.csv` | `channels.csv` |

### 1.3 `gohookbridge/store/raft.go`
| Before | After |
|--------|-------|
| `GetProject()` | `GetChannel()` |
| `ListProjects()` | `ListChannels()` |
| `CreateProject()` | `CreateChannel()` |
| `UpdateProject()` | `UpdateChannel()` |
| `DeleteProject()` | `DeleteChannel()` |
| `ResolveProjectConfig()` | `ResolveChannelConfig()` |
| `ResolveProjectSignatures()` | `ResolveChannelSignatures()` |
| `ResolveProjectAllowedIPs()` | `ResolveChannelAllowedIPs()` |
| `ResolveProjectMaxBodySize()` | `ResolveChannelMaxBodySize()` |
| `ResolveProjectEncryption()` | `ResolveChannelEncryption()` |
| `UserProjects()` | `UserChannels()` |
| BoltDB key prefix `/projects/` | `/channels/` |
| `defaultGlobalConfig()` → `Defaults: DefaultProjectConfig{}` | `Defaults: DefaultChannelConfig{}` |
| FSM command `"create-project"` | `"create-channel"` |
| All local vars: `projects`, `project`, `projectID` | `channels`, `channel`, `channelID` |
| Error: `"project ID required"` | `"channel ID required"` |
| Error: `"project %q not found"` | `"channel %q not found"` |

### 1.4 `gohookbridge/store/fsm.go`
- `"create-project"` → `"create-channel"`
- `applyCreateProject()` → `applyCreateChannel()`
- Error `"project %q already exists"` → `"channel %q already exists"`

### 1.5 `gohookbridge/store/bridge.go`
- `NewProtectedChannels()`: local var `projects` → `channels`
- `ProtectedChannels.Has()`: local var `project` → `channel`
- `ProtectedChannels.IsAllowed()`: local var `project` → `channel`
- `User.Projects` → `User.Channels` (in `BuildAuthConfig` if any)

### 1.6 `gohookbridge/server.go`
- `rs.ResolveProjectMaxBodySize()` → `rs.ResolveChannelMaxBodySize()`
- `rs.ResolveProjectSignatures()` → `rs.ResolveChannelSignatures()`
- `rs.ResolveProjectAllowedIPs()` → `rs.ResolveChannelAllowedIPs()`
- Error messages in webhook handlers (`"project not found"` → `"channel not found"`)

### 1.7 `gohookbridge/*_test.go` files
All test files (`server_test.go`, `client_test.go`, `replay_test.go`, `store/raft_test.go`, `store/fsm_test.go`, `store/bootstrap_test.go`, `store/acl_test.go`, `store/bolt_test.go`, `store/storetest/helper.go`):
- `store.Project{}` → `store.Channel{}`
- `.CreateProject()` → `.CreateChannel()`
- `.UpdateProject()` → `.UpdateChannel()`
- `.DeleteProject()` → `.DeleteChannel()`
- `.GetProject()` → `.GetChannel()`
- `PermProjectRead`/`Write`/`View` → `PermChannelRead`/`Write`/`View`
- All local variable renames

### 1.8 `gohookbridge/client.go` / `gohookbridge/replay.go` / `gohookbridge/auth.go`
- Any `project`/`Project` references → `channel`/`Channel`

---

## Phase 2 — Frontend rename

### 2.1 `web/src/api/client.ts`
| Before | After |
|--------|-------|
| `Project` interface | `Channel` |
| `listProjects()` | `listChannels()` |
| `getProject()` | `getChannel()` |
| `createProject()` | `createChannel()` |
| `updateProject()` | `updateChannel()` |
| `deleteProject()` | `deleteChannel()` |
| URL `/projects` | `/channels` |
| URL `/projects/${id}` | `/channels/${id}` |
| `User.projects: string[]` | `User.channels: string[]` |
| `Binding.projects: string[]` | `Binding.channels: string[]` |
| `createUser` param `projects` | `channels` |

### 2.2 `web/src/stores/channels.ts`
- `type Project` → `type Channel`
- `api.listProjects()` → `api.listChannels()`
- `api.createProject()` → `api.createChannel()`
- `api.updateProject()` → `api.updateChannel()`
- `api.deleteProject()` → `api.deleteChannel()`
- `createChannel(id, name?)` → `createChannel(id, description?)` (see Phase 4)

### 2.3 `web/src/views/ChannelView.vue`
- `type Project` → `type Channel`
- `api.getProject()` → `api.getChannel()`
- `api.updateProject()` → `api.updateChannel()`
- `project` ref → `channel` ref

### 2.4 `web/src/views/DashboardView.vue`
- `type Project` → `type Channel`
- Render function types

### 2.5 `web/src/views/AdminUsersView.vue`
- `form.projectsStr` → `form.channelsStr`
- Label "Projects" → "Channels"
- `payload.projects` → `payload.channels`
- `user.projects` → `user.channels`

### 2.6 `web/src/views/AdminRBACView.vue`
- `form.projectsStr` → `form.channelsStr`
- Label "Projects" → "Channels"
- Binding payload

### 2.7 `web/src/stores/auth.ts`
- `UserInfo.projects` → `UserInfo.channels`

---

## Phase 3 — Add validator/v10 dependency

### 3.1 `go.mod`
Add `github.com/go-playground/validator/v10`, run `go mod tidy`.

### 3.2 `gohookbridge/store/types.go` — validator setup
```go
import (
    "regexp"
    "github.com/go-playground/validator/v10"
)

var validate *validator.Validate

func init() {
    validate = validator.New()
    validate.RegisterValidation("channelid", func(fl validator.FieldLevel) bool {
        return regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`).MatchString(fl.Field().String())
    })
}
```

### 3.3 Struct tags
```go
type Channel struct {
    ID          string   `json:"id" validate:"required,min=1,max=64,channelid"`
    Name        string   `json:"name"`
    Description string   `json:"description,omitempty" validate:"max=500"`
    // ... rest unchanged
}

type User struct {
    ID       string `json:"id" validate:"required,min=1,max=128"`
    Username string `json:"username" validate:"required,min=1,max=128"`
    // ...
}

type UserBinding struct {
    UserID   string   `json:"user_id" validate:"required"`
    // ...
}
```

---

## Phase 4 — Add description + fix channel creation

### 4.1 `gohookbridge/store/types.go`
Add `Description string` to `Channel` (done in Phase 3 with validate tags).

### 4.2 `gohookbridge/store/api.go` — validateStruct helper + handler updates
```go
func validateStruct(s interface{}) string {
    if err := validate.Struct(s); err != nil {
        var msgs []string
        for _, e := range err.(validator.ValidationErrors) {
            msgs = append(msgs, fmt.Sprintf("%s: %s", e.Field(), e.Tag()))
        }
        return strings.Join(msgs, "; ")
    }
    return ""
}
```

#### `createChannel`:
```go
var ch Channel
// decode JSON
if msg := validateStruct(&ch); msg != "" {
    writeError(w, http.StatusBadRequest, msg)
    return
}
// create
```

#### `updateChannel`:
Same validation after decode.

#### `createUser` input:
```go
var input struct {
    Username string   `json:"username" validate:"required,min=1,max=128"`
    Password string   `json:"password"`
    Roles    []string `json:"roles"`
    Channels []string `json:"channels"`
}
// Use validateStruct instead of manual input.Username == ""
```

#### `updateUser` input:
```go
var input struct {
    Username string   `json:"username" validate:"max=128"` // optional on update
    Password string   `json:"password,omitempty"`
    Roles    []string `json:"roles"`
    Channels []string `json:"channels"`
}
```

#### `updateBinding`:
```go
var binding UserBinding
// decode, validateStruct, apply
```

### 4.3 `web/src/api/client.ts`
Add to `Channel`:
```ts
description?: string
```

### 4.4 `web/src/stores/channels.ts`
```ts
async function createChannel(id: string, description?: string) {
    await api.createChannel({ id, description })
    await fetchChannels()
}
```
(remove `name` parameter — name is a display label set after creation)

### 4.5 `web/src/views/DashboardView.vue` — rewrite modal

Client-side validation rules:
```ts
const rules = {
  id: [
    { required: true, message: 'Channel ID required', trigger: 'blur' },
    { pattern: /^[a-zA-Z0-9][a-zA-Z0-9_-]*$/, message: 'Letters, numbers, hyphens, underscores only', trigger: 'blur' },
    { max: 64, message: 'Max 64 characters', trigger: 'blur' },
  ],
  description: [
    { max: 500, message: 'Max 500 characters', trigger: 'blur' },
  ],
}
```

Modal state:
```ts
const formData = reactive({ id: '', description: '' })
const creating = ref(false)
```

Modal template:
```vue
<n-modal v-model:show="showCreate" title="New Channel" preset="card" style="width: 400px;">
  <n-form ref="formRef" :model="formData" :rules="rules" @submit.prevent="handleCreate">
    <n-form-item label="Channel ID" path="id">
      <n-input v-model:value="formData.id" placeholder="my-webhook-channel" :maxlength="64" />
    </n-form-item>
    <n-form-item label="Description" path="description">
      <n-input v-model:value="formData.description" placeholder="Optional" type="textarea" :maxlength="500" />
    </n-form-item>
    <n-space justify="end">
      <n-button @click="showCreate = false">Cancel</n-button>
      <n-button type="primary" attr-type="submit" :loading="creating">Create</n-button>
    </n-space>
  </n-form>
</n-modal>
```

Handler:
```ts
async function handleCreate(e: Event) {
  e.preventDefault()
  try {
    await formRef.value?.validate()
  } catch { return }
  creating.value = true
  try {
    await channelsStore.createChannel(formData.id, formData.description || undefined)
    message.success('Channel created')
    showCreate.value = false
    formData.id = ''
    formData.description = ''
  } catch (e: any) {
    message.error(e.message)
  } finally {
    creating.value = false
  }
}
```

Remove old state: `newChannelId`, `newChannelName`.

---

## Phase 5 — Tests

### 5.1 `gohookbridge/store/api_test.go` (new)
Test via httptest + chi router:
- `TestCreateChannel_ValidID`: `my-channel`, `test_hook`, `a`, 63-char → 201
- `TestCreateChannel_InvalidID_Empty` → 400 "ID: required"
- `TestCreateChannel_InvalidID_Spaces` (`"my channel"`) → 400 "ID: channelid"
- `TestCreateChannel_InvalidID_SpecialChars` (`"my@ch"`) → 400
- `TestCreateChannel_InvalidID_StartsWithSpecial` (`"-chan"`, `"_test"`) → 400
- `TestCreateChannel_InvalidID_TooLong` (65 chars) → 400 "ID: max"
- `TestCreateChannel_WithDescription` → 201, GET confirms round-trip
- `TestCreateChannel_DescriptionTooLong` → 400
- `TestCreateChannel_Duplicate` → 409

### 5.2 Update existing tests
All existing test files must use new names (`Channel`, `CreateChannel`, etc.) — done as part of Phase 1 rename.

---

## Files modified summary

### Go backend (14 files)
| File | Changes |
|------|---------|
| `go.mod` | Add validator/v10 |
| `gohookbridge/store/types.go` | Channel struct, permissions, roles, validator init |
| `gohookbridge/store/api.go` | Routes, handlers, validateStruct helper, validator calls |
| `gohookbridge/store/raft.go` | Method renames, bolt keys |
| `gohookbridge/store/fsm.go` | FSM command rename |
| `gohookbridge/store/bridge.go` | Variable renames |
| `gohookbridge/server.go` | Method call renames |
| `gohookbridge/server_test.go` | Test renames |
| `gohookbridge/client_test.go` | Test renames |
| `gohookbridge/replay_test.go` | Test renames |
| `gohookbridge/store/raft_test.go` | Test renames + ValidateChannelID tests |
| `gohookbridge/store/api_test.go` | New: handler tests |
| `gohookbridge/store/fsm_test.go` | Test renames |
| `gohookbridge/store/acl_test.go` | Permission renames |
| `gohookbridge/store/bootstrap_test.go` | Test renames |
| `gohookbridge/store/bolt_test.go` | Test renames |
| `gohookbridge/store/storetest/helper.go` | Helper renames |

### Frontend (7 files)
| File | Changes |
|------|---------|
| `web/src/api/client.ts` | Channel interface, method renames, description field |
| `web/src/stores/channels.ts` | Type + method call renames, createChannel signature |
| `web/src/stores/auth.ts` | UserInfo.channels rename |
| `web/src/views/DashboardView.vue` | Modal rewrite with validation + description |
| `web/src/views/ChannelView.vue` | Type + method call renames |
| `web/src/views/AdminUsersView.vue` | projectsStr → channelsStr, labels |
| `web/src/views/AdminRBACView.vue` | projectsStr → channelsStr, labels |

---

## Implementation order
1. `go.mod` — add `validator/v10`
2. `gohookbridge/store/types.go` — rename + validator setup + Channel struct
3. `gohookbridge/store/raft.go` — rename all methods/keys
4. `gohookbridge/store/fsm.go` — rename FSM command
5. `gohookbridge/store/api.go` — rename routes/handlers + add validation
6. `gohookbridge/store/bridge.go` — rename vars
7. `gohookbridge/server.go` — rename method calls
8. All `*_test.go` files — rename + add new tests
9. Frontend files — rename + add description + rewrite modal
10. `go mod tidy`
