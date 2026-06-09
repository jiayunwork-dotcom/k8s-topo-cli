package audit

import (
	"fmt"
	"os"
	"text/template"
)

func RenderAuditReportWithTemplate(report *AuditReport, templatePath string) error {
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New("audit").Parse(string(data))
	if err != nil {
		fmt.Fprintf(os.Stderr, "template parse error: %v\n", err)
		return fmt.Errorf("template parse error: %w", err)
	}

	if err := tmpl.Execute(os.Stdout, report); err != nil {
		return fmt.Errorf("template execution error: %w", err)
	}

	return nil
}
