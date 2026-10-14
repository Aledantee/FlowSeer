{{- if not .OnCluster -}}
-- A plain MergeTree table deduplicates an insert block only when
-- non_replicated_deduplication_window is above its default of 0. Without it
-- the insert_deduplication_token of a retried batch does nothing on a single
-- node. A cluster table keeps its own window, so the whole file renders to
-- nothing there and the migrator records the step without executing it.
ALTER TABLE {{.DB}}.syslog MODIFY SETTING non_replicated_deduplication_window = 1000
{{- end}}
