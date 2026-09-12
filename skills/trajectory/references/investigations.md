# Saved investigations

Resolve `TRAJECTORY_BIN` as described in SKILL.md. These commands work from any
workspace; add `--url` for a different local service.

## Read frozen evidence

For a link containing `investigation=ID`, preserve that ID and any `span` value:

```sh
"$TRAJECTORY_BIN" investigation view 'INVESTIGATION_ID' --span 'SPAN_ID'
"$TRAJECTORY_BIN" investigation review 'INVESTIGATION_ID' --details
"$TRAJECTORY_BIN" investigation spans 'INVESTIGATION_ID' --sort duration --limit 10
"$TRAJECTORY_BIN" investigation span 'INVESTIGATION_ID' --span 'SPAN_ID' --maxChars 2000
```

Omit `--span` when the link has none. Read the saved question, filters, selected
operation, and coverage. Continue through `investigation` commands so live task
updates cannot change the evidence. Saved review inherits its turn/category
filters; pass `--turnId ''` or `--category ''` only when intentionally broadening
the question. `view --span` reveals a cited operation even outside saved filters.

## Capture and record a finding

When durable evidence is needed within the requested investigation:

```sh
"$TRAJECTORY_BIN" investigation create --session 'TASK_ID' --span 'SPAN_ID' --turnId 'TURN_ID' --mode review --question 'What explains this cost?'
```

The selected span must belong to the scoped turn. Omit `--turnId` for a whole-task
review. Capture returns an investigation ID and local URL. Explore the resulting
snapshot before attaching a finding; it may be newer than a previous live read.
When preserving a displayed view, use its `--revision` and `--view-file` instead
of silently taking a new frame. Expired revisions require a refresh; oversized
captures may be unavailable. When preserving an exact displayed state, retain the supplied view file unchanged;
consult `API.md` in the checkout for the saved-view schema if constructing one.

Write a JSON file with `title`, `observation`, `hypothesis`, `recommendation`,
and `evidence`. Each text field is limited to 2,000 characters; evidence must
contain one to eight span IDs from this snapshot. Then attach it:

```sh
"$TRAJECTORY_BIN" investigation finding 'INVESTIGATION_ID' --file '/tmp/trajectory-finding.json'
```

Return the resulting evidence URL. It reopens the frozen data and depends on
this local service and database; it is not a publicly hosted share link.
