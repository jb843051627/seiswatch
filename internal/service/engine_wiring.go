package service

// AttachEngine wires a QC engine into the ingest pipeline.
func AttachEngine(ing *IngestService, e *QCEngine) {
	ing.SetEngine(e)
}
