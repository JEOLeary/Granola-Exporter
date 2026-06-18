# {{.Meeting.Title}}

Date/Time: {{.Meeting.StartDateTimeFormatted}}{{if .Meeting.DurationFormatted}} ({{.Meeting.DurationFormatted}}){{end}}<br />ID: {{.Meeting.ID}}

## Notes

{{if .Meeting.Notes}}{{.Meeting.Notes}}{{else}}*No notes*{{end}}

---

## Transcript

{{if .Meeting.Transcript}}{{.Meeting.Transcript}}{{else}}*No transcript*{{end}}
