# Resolution fixtures

Each directory is an application configuration, not a separate engine detector.
Tests supply a shared effective Maven model containing H2, MySQL, and PostgreSQL
runtime drivers; no Maven execution or Azure access is required.

| Fixture | Expected decision |
| --- | --- |
| `h2-mysql` | None: default H2 wins; inactive MySQL is not ambiguity. |
| `h2-postgres` | None: default H2 wins; inactive PostgreSQL is not ambiguity. |
| `three-engines` | None: H2 wins by default; explicit profiles select MySQL/PostgreSQL without questions. |
| `default-postgres` | None: application-selected PostgreSQL wins despite alternatives. |
| `profile-choice` | Ambiguous: a required profile selector is missing; three profile files establish choices affecting the application. |
| `default-profile-choice` | Ambiguous: the default-profile selector is missing; explicit selection is pinned at runtime without re-evaluating that placeholder. |
| `missing-url` | Missing input, not engine ambiguity: the datasource URL is unspecified; drivers do not establish choices. |
| `missing-port` | Missing input: HTTP port; no resource selection question. |
| `prompt-driver` | Missing input: `DB_ENGINE` is shared by the URL and driver. One answer resolves both; deployment must preserve the resolved driver without exporting that placeholder. |
| `driver-conflict` | Invalid: H2 URL with PostgreSQL driver; never offer a prompt to repair it. |
| `file-storage` | Unsupported: file-based H2 needs a persistence policy; never prompt for an alternative engine. |

Tests also verify ambient Spring variables are ignored, explicit answers re-run
analysis, pending decisions block generation, and embedded URLs survive generation.
Command tests use fake host prompts to check interactive/non-interactive behavior.
Non-interactive missing values must be resolved in project configuration; only
profile selection has a CLI override.
Multi-application/custom-datasource detection is intentionally outside this suite.
