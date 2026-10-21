package engine

// RegisterAll 把全部内置引擎注册到注册表。
// 新增引擎只需在这里追加一次注册调用。
func RegisterAll(reg *Registry) {
	RegisterApibay(reg)
	RegisterBitsearch(reg)
	RegisterTokyotosho(reg)
	RegisterRutor(reg)
	RegisterBt4g(reg)
	RegisterNyaa(reg)
	RegisterYts(reg)
	RegisterSolidtorrents(reg)
}
