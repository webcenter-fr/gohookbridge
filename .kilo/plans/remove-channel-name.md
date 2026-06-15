# Plan: Remove Channel `Name` field, keep only `ID`

## Motivation

The `Channel` struct has both `ID` (required, validated identifier used everywhere) and `Name` (optional, free-text display label). The `Name` field:

- Cannot be set at creation time (create modals only expose `id` + `description`)
- Defaults to empty, so the UI falls back to `ch.name || ch.id` everywhere
- Is used in zero backend operations (only UI display and CSV export)
- Creates confusion about which field to use for what purpose

Removing `Name` simplifies the data model without any functional loss — `ID` already serves as both the technical and display identifier.

## Backend Changes (Go)

### 1. `gohookbridge/store/types.go:12`
Remove the `Name` field from `Channel` struct:
```go
Name string `json:"name"`
```

### 2. `gohookbridge/store/api.go:459-466` (`writeCSV`)
- Change CSV header from `"id,name\n"` to `"id\n"`
- Change row format from `fmt.Fprintf(w, "%s,%s\n", ch.ID, ch.Name)` to `fmt.Fprintf(w, "%s\n", ch.ID)`

### 3. `gohookbridge/store/bootstrap.go:33`
Remove `Name` field from `BootstrapChannel` struct:
```go
Name string `yaml:"name" json:"name"`
```

### 4. `gohookbridge/store/bootstrap.go:83`
Remove `Name: p.Name,` from channel construction in `ApplyBootstrap`.

### Backend Test Files

5. **`gohookbridge/store/api_test.go:169`** — Remove `"name":"updated"` from the PUT request JSON body
6. **`gohookbridge/store/api_test.go:174`** — Remove `assert.Equal(t, got.Name, "updated")`
7. **`gohookbridge/store/raft_test.go:35`** — Remove `Name: "test"` from channel creation
8. **`gohookbridge/store/raft_test.go:45`** — Remove `Name: "Project One"` from channel creation
9. **`gohookbridge/store/raft_test.go:52`** — Remove `assert.Equal(t, got.Name, "Project One")`
10. **`gohookbridge/store/raft_test.go:61`** — Remove `Name: "Original"` from channel creation
11. **`gohookbridge/store/raft_test.go:64`** — Remove `Name: "Updated"` from channel update (keep `MaxBodySize: 999`)
12. **`gohookbridge/store/raft_test.go:69`** — Remove `assert.Equal(t, got.Name, "Updated")`
13. **`gohookbridge/store/raft_test.go:76`** — Remove `Name: "To Delete"` from channel creation
14. **`gohookbridge/store/bootstrap_test.go:26`** — Remove `name: Project One` line from YAML fixture
15. **`gohookbridge/store/bootstrap_test.go:46`** — Remove `assert.Equal(t, cfg.Channels[0].Name, "Project One")`
16. **`gohookbridge/store/bootstrap_test.go:63`** — Remove `"name": "Project One"` from JSON fixture
17. **`gohookbridge/store/bootstrap_test.go:157-158`** — Remove `Name: "Project A"` and `Name: "Project B"` from BootstrapChannel values
18. **`gohookbridge/store/bootstrap_test.go:171`** — Remove `assert.Equal(t, gotA.Name, "Project A")`
19. **`gohookbridge/store/bootstrap_test.go:175`** — Remove `assert.Equal(t, gotB.Name, "Project B")`
20. **`gohookbridge/store/bootstrap_test.go:183`** — Remove `Name: "One"` from BootstrapChannel

## Frontend Changes (Vue / TypeScript)

21. **`web/src/api/client.ts:15`** — Remove `name: string` from `Channel` interface
22. **`web/src/views/ChannelDetailView.vue:28-30`** — Remove entire "Name" form item (label + input)
23. **`web/src/views/ChannelDetailView.vue:323`** — Remove `form.name = channel.value.name || ''` line
24. **`web/src/views/ChannelDetailView.vue:355`** — Remove `name: form.name,` from `handleSave` payload
25. **`web/src/views/DashboardView.vue:20`** — Change `:title="ch.name || ch.id"` to `:title="ch.id"`
26. **`web/src/views/ChannelsView.vue:65`** — Remove `(ch.name && ch.name.toLowerCase().includes(q))` from search (keep only ID search)
27. **`web/src/views/ChannelsView.vue:82`** — Remove `{ title: 'Name', key: 'name' as const }` column from table

## Documentation

28. **`SECURITY.md:19`** — `channel name limits` → `channel ID length limits`
29. **`SECURITY.md:37`** — `channel name length limit` → `channel ID length limit`
30. **`SECURITY.md:137`** — `any channel they can name` → `any known channel`
31. **`README.md:497`** — `channel name protection` → `channel ID validation`
