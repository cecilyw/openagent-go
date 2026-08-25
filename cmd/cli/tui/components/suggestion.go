package components

var suggestionList = []string{
	"做一个关于 AI 发展趋势的 PPT",
	"你有什么能力？",
	"今天有什么新闻？",
	"帮我搜一下 Go 1.26 的新特性",
	"读一下当前项目结构，给我一个概览",
	"用 plan 模式拆解这个任务，先出计划",
}

var suggestionIndex = 0

// SetSuggestions replaces the built-in suggestion list used by the
// welcome-page placeholder. A nil or empty slice keeps the defaults.
// Must be called before NewModel (which consumes the first suggestion
// via NextSuggestion).
func SetSuggestions(list []string) {
	if len(list) == 0 {
		return
	}
	suggestionList = list
	suggestionIndex = 0
}

func NextSuggestion() string {
	nextIndex := suggestionIndex + 1
	if nextIndex >= len(suggestionList) {
		nextIndex = 0
	}
	result := suggestionList[suggestionIndex]
	suggestionIndex = nextIndex
	return result

}
