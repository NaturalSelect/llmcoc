// scripter_delta.go — IronyCore type kept for backward compatibility.
//
// The δ-operator cognitive twist taxonomy has been removed. IronyCore remains
// as an empty struct so that ScenarioCreationOutput.IronyCore does not break
// existing API consumers and database records.
package agent

// ---------------------------------------------------------------------------
// IronyCore — kept for ScenarioCreationOutput backward compatibility.
// ---------------------------------------------------------------------------

type IronyCore struct {
}
