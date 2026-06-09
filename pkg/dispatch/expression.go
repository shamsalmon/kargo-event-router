package dispatch

import (
	"fmt"
	"strings"

	"github.com/expr-lang/expr"
	corev1 "k8s.io/api/core/v1"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// evalWhen evaluates the given expr-lang expression against the given
// Kubernetes Event and returns whether it matched. The expression sees an
// `event` object exposing the event's type, project, and message, plus all
// of its Kargo annotations as camelCase fields (e.g. the
// event.kargo.akuity.io/stage-name annotation becomes event.stageName).
func evalWhen(when string, evt *corev1.Event) (bool, error) {
	result, err := expr.Eval(when, map[string]any{"event": exprEvent(evt)})
	if err != nil {
		return false, fmt.Errorf("error evaluating expression %q: %w", when, err)
	}
	matched, ok := result.(bool)
	if !ok {
		return false, fmt.Errorf(
			"expression %q did not evaluate to a boolean", when,
		)
	}
	return matched, nil
}

// exprEvent builds the `event` object visible to when expressions.
func exprEvent(evt *corev1.Event) map[string]any {
	event := map[string]any{
		"type":    evt.Reason,
		"project": evt.Namespace,
		"message": evt.Message,
	}
	for key, value := range evt.Annotations {
		if name, ok := strings.CutPrefix(
			key, kargoapi.AnnotationKeyEventPrefix,
		); ok {
			event[camelCase(name)] = value
		}
	}
	return event
}

// camelCase converts a kebab-case annotation name like "stage-name" to
// "stageName".
func camelCase(s string) string {
	words := strings.Split(s, "-")
	for i := 1; i < len(words); i++ {
		if words[i] != "" {
			words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
		}
	}
	return strings.Join(words, "")
}
