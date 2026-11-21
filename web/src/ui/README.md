# UI kit

Every page is built from these pieces. Import them from `'../ui'` (or `'./ui'`), never from a
file inside this folder, and never rebuild one by hand: a hand-made header or form row is how
fields end up misaligned on one screen and not the others.

## Rules

- **Twice means shared.** If a piece of layout shows up on two screens, it becomes a component
  here (or in `status.ts` if it's a state or a label).
- **No inline styles.** `style={{…}}` may only set CSS custom properties (`'--pct'`), for values
  that are data. Everything else is a class or a prop (`<Field tone="bad">`).
- **No hex colors** in components or CSS: only M3 roles (`var(--md-*)` and the aliases in
  `styles/tokens.css`). Themes swap them.
- **Files under 300 lines.** One page, one dialog or one job per file. Pages live in a folder per
  area (`apps/`, `databases/`, `projects/`, `workspaces/`, `settings/`, `nodes/`).
- **CSS by area** in `src/styles/`, imported in cascade order by `styles.css`. New classes are
  prefixed by their component (`.popmenu-*`, `.svc-*`).
- **Check** every change at 375 px (no horizontal scroll), in light and dark, and with a
  non-default theme. On phones, dialogs are bottom sheets and row actions go in a ⋮ sheet.

`bun run check` (part of `bun run build`) fails on an inline style, a hex color or a file over
300 lines. `bun test` runs the unit tests in `web/test/`.

## Pieces

| File | Holds |
|---|---|
| `layout.tsx` | `PageHeader`, `PageBar`, `Section`, `Note`, `Tabs`, `DangerZone` |
| `form.tsx` | `Field`, `FormRow`, `Select`, `Check`, `Seg`, `Disclosure` |
| `fields.tsx` | `NodeSelect`, `ProjectField`, `EnvEditor`, `EnvModal` |
| `overlay.tsx` | `Modal`, `Foot`, `Confirm`, `PopMenu`, `RowActions`, `ActionSheet` |
| `feedback.tsx` | `Badge`, `Alert`, `ErrorAlert`, `EmptyState`, `Loading`, `LoadError`, `LoadFailed`, toasts |
| `data.tsx` | `Chip`, `Meter`, `CopyBtn`, `Cmd` |
| `icons.tsx`, `logos.tsx` | `Icon`, `Logo`; `BrandLogo`, `Mark` (Simple Icons, CC0) |
| `hooks.ts` | `usePoll`, `ago`, `useTrail` (breadcrumbs) |
| `auth.ts` | `useCan`, `allowed`, `MeCtx` (mirrors `internal/auth/permissions.go`) |

States and labels of resources (`appState`, `dbState`, `engineLabel`, `duration`…) are in
`src/status.ts`, so a service shows the same state in its project and on its own page.

## Examples

A resource page:

```tsx
if (error && !a) return <LoadFailed what="app" error={error} notFound={error === 'app not found'} back="#/projects" backLabel="Back to projects" />;
if (!a) return <Loading />;
const [s, label] = appState(a);
return <>
  <PageHeader mono title={a.name} badge={<Badge s={s} label={label} />} mark={<Icon n="apps" />}
    meta={<span>{a.description}</span>} actions={<button className="btn btn-primary">Deploy</button>} />
  <Tabs tabs={TABS} value={tab} onChange={setTab} />
  <Section id="src-h" title="Source" sub="Where the app comes from." actions={<button className="btn">Edit</button>}>
    <dl className="kv">…</dl>
  </Section>
</>;
```

A form dialog:

```tsx
<Modal title="Create database" onClose={onClose}
  foot={<Foot onCancel={onClose} onClick={submit} busy={busy} disabled={!name} label="Create" busyLabel="Creating…" />}>
  {error && <ErrorAlert title="Couldn't create it" msg={error} />}
  <div className="form">
    <FormRow>
      <Field label="Name" hint="A DNS label."><input className="input mono" value={name} onChange={…} /></Field>
      <Field label="Version"><Select value={version} onChange={setVersion}>…</Select></Field>
    </FormRow>
    <Disclosure summary="More options">
      <Field label="Description" optional><input className="input" … /></Field>
      <NodeSelect role="prod" value={node} onChange={setNode} />
    </Disclosure>
  </div>
</Modal>
```

A `Seg` (a group of buttons) goes in `<Field label="Runtime" group>`: a `<label>` would press the first button whenever its text is clicked.


A list with row actions (icon buttons on wide screens, a ⋮ sheet on phones):

```tsx
const actions = (w: Workspace): Action[] => [
  { icon: 'plug', label: 'Connect', run: () => connect(w) },
  { icon: 'trash', label: 'Delete', run: () => remove(w), danger: true },
];
<td className="hide-xs"><RowActions name={w.name} actions={actions(w)} /></td>
<td className="show-xs"><button className="btn-icon" onClick={() => setSheet(w)}><Icon n="more" /></button></td>
{sheet && <ActionSheet title={sheet.name} actions={actions(sheet)} onClose={() => setSheet(null)} />}
```
