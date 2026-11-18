# Payload schema validation extension

A `PRE_RESUME` task extension that checks a step submission against the JSON Schema of
the form the user filled in. The trader app already validates every form in the
browser; this extension applies the same schema on the server, so a call that skips the
browser is held to the same rules.

It runs wherever core runs `PRE_RESUME` extensions for a step that lists it: submissions
(`POST /api/v1/tasks/{id}/steps/{stepId}`), callbacks (`POST /api/v1/callbacks/{token}`)
and draft saves (`PUT /api/v1/tasks/{id}/steps/{stepId}/draft`).

## Enabling it on a step

List the extension on the step template, naming the form a submission must match in its
`form` property.

```json
{
  "id": "npqs-application-review--officer-review",
  "task_type": "USER_INPUT",
  "output_namespace": "reviewform",
  "plugin_properties": {
    "user_jsonforms_id": "npqs-apply-phyto-cert--reviewer-form"
  },
  "extensions": [
    {
      "id": "payload_schema",
      "phase": "PRE_RESUME",
      "properties": { "form": "npqs-apply-phyto-cert--reviewer-form" }
    }
  ]
}
```

The extension never reads `plugin_properties`; name the form the user fills in, which
on a `USER_INPUT` step is usually the one `user_jsonforms_id` shows.

Removing it from the step template turns validation off for that step. A step template
that lists it while the running backend doesn't register it fails every submission on
that step, so the artifacts must not list it before a release that has it is deployed.

## What it checks

The submission, without core's system keys (those prefixed `__`, such as `__command`), is
validated against the form's `schema`, read the way the trader app's Ajv instance reads
it:

- draft-07 when the schema declares no `$schema`;
- formats asserted, with ajv-formats' rules for `time` (timezone optional) and `email`
  (dot required in the domain); formats neither side defines, such as `file`, are ignored;
- ajv-formats' `formatMaximum`, `formatMinimum` and their exclusive forms as the forms use
  them: on a `date`, with the limit read from a sibling through Ajv's `$data`
  (`{"$data": "1/<sibling>"}`), so one date can't be later than another. Only that use is
  checked; a limit written in the schema, on another format, read from elsewhere, or on a
  value that isn't an object's property is left to the browser.
- standard JSON Schema semantics: `required` only checks that a key is present.

The schema is compiled on every call, as the other artifact templates are parsed on every
use, so an edited form applies at once.

### Read-only values

A value the form's schema marks `readOnly` is one the server put there, shown greyed out
(a UI schema `readonly` option is display only and is not read). The submission must
send it back as the step stores it: under the step template's `output_namespace` in the
task data, which core fills when the step starts and replaces with a draft or a
submission.

- **A single value:** one the step stores comes back unchanged; one it doesn't is left
  out, or sent as its schema `default`, which the trader app fills in by itself. A list
  or object marked `readOnly` as a whole comes back exactly as stored.
- **A list whose rows hold read-only values:** rows pair with the stored rows by those
  values, in any order, taking a missing one as its default on both sides; paired rows
  are then checked value by value. A row pairing with none is a new row only if it
  carries no read-only values. Rows are added or removed only as the list's UI schema
  control allows (`addable`, `removable`, both on unless set to `false`; a list no
  control shows allows neither). Lists inside rows follow the same rule.

The schema is followed through `properties`, `allOf`, `$ref`, `additionalProperties`,
`patternProperties` and list `items`. A `readOnly` under a part whose application depends
on the data or a row's position (`if`/`then`/`else`, `oneOf`, `anyOf`, `not`,
`dependencies` in its schema form, `dependentSchemas`, `contains`, `propertyNames`, tuple
`items`, `prefixItems`, `additionalItems`, `unevaluated*`, `$dynamicRef`, `$recursiveRef`)
makes the form misconfigured. So does, in a form with read-only values, a `default` or a
keyword holding a `readOnly` (such as `properties` or `allOf`) beside a draft-07 `$ref`,
which the library drops; a `readOnly` beside a `$ref` is kept.

### File fields (`x-file`)

A file field is one the trader app's file control renders: one file
(`{"type": "string", "format": "file"}`) or a list of them (`{"type": "array", "items":
{"type": "string", "format": "file"}}`). Its `x-file` rules are checked against the stored
values the submission carries, wherever the schema applies to the field, in list rows and
conditional parts too. Nothing is looked up in storage:

```json
"supporting_documents": {
  "type": "array",
  "items": { "type": "string", "format": "file" },
  "x-file": { "accept": ".pdf,.png", "maxFiles": 5 }
}
```

- **An empty value names no file** and fails with `empty value instead of a file`; the
  trader app drops a field that holds no file rather than sending `""`.
- **`accept`** is matched as the file control matches a file's name, against the stored
  value: `*` and `*/*` take any file, and `.<ext>` a value that ends with it, in any case.
  The value ends with the file's extension, which storage matched to its type on upload:
  on TNSW it is the storage key, and on an agency the owning service's reference, which
  carries the extension too. An entry that names a type (`image/*`, `application/pdf`)
  matches nothing, as the value doesn't carry the type.
- **`maxFiles`** is the most files a list may hold.
- **Read-only values aren't checked.** A value the read-only check holds (one whose
  schemas, or those of a value holding it, mark it `readOnly`) must come back as the step
  stored it. Its files came in through another form, under that form's rules, so this
  form's `x-file` doesn't judge them.

Only the rules `x-file` writes are checked, other than `maxSize` (see below). A rule it
leaves out, and a file field without `x-file`, is held to the deployment's upload limits only
(`storage.allowedUploadTypes`, `storage.maxUploadBytes`); the file control's defaults and
UI schema `options` apply in the browser only. A rule of the wrong type means what it
means to the file control: an `accept` set to something other than text takes no file
(`null` counts as not set), and a `maxFiles` given as text is the number JavaScript reads
it as, or no limit.

## Outcome

- **Matches:** the step goes ahead.
- **Doesn't match:** `400` with the fixed message `submission does not match the form`.
  The failing fields of both checks (JSON pointer and the rule that failed, never a
  value) are logged together at warn level, not returned:
  the browser checks the same schema first, so this is either a form the browser and the
  server disagree on, or a call that skipped the browser.
- **Misconfigured** (no `form` in the extension's properties, an unknown form, a form
  without a `schema`, a schema that doesn't compile, a `readOnly` where the check doesn't
  follow it, or read-only values on a step without an `output_namespace`): `500`. The
  extension fails closed; it never lets a submission through unchecked.

## Drafts

A draft may be incomplete, so draft saves are not held to the form's schema. They are
held to the read-only rules, since a draft replaces the stored values those compare
against. The draft route marks its request context (`WithDraft`), and the extension knows
a draft by that mark, never by the command: a submission that names the step's draft
command is validated in full.

## Not covered yet

- Fields the form doesn't declare
- External reviewer callbacks, and schemas built at render time
- `x-file`'s `maxSize`: a file's size is the upload's concern, not the submission's. The
  browser checks it, and storage holds every file to the upload limit
  (`storage.maxUploadBytes`).
- What a file holds, and whether its upload was made: a file is checked by its stored
  value alone, whose extension is the one it was uploaded with.
