package relaymode

const (
	Unknown = iota
	ChatCompletions
	Completions
	Embeddings
	Moderations
	ImagesGenerations
	Edits
	AudioSpeech
	AudioTranscription
	AudioTranslation
	// Rerank is for semantic re-ranking of documents (Cohere/Jina/BGE style)
	Rerank
	// Proxy is a special relay mode for proxying requests to custom upstream
	Proxy
)
