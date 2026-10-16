package aiproxy

import "github.com/xiaosu4610/LTZY-API/relay/adaptor/openai"

var ModelList = []string{""}

func init() {
	ModelList = openai.ModelList
}
