# {{.Title}}

Date/Time: {{.DateTimeFormatted}}<br />
Meeting ID: {{.MeetingID}}

## Notes

{{if .Notes}}{{.Notes}}{{else}}*No notes*{{end}}

---

## Transcript

{{if .Transcript}}{{.Transcript}}{{else}}*No transcript*{{end}}
