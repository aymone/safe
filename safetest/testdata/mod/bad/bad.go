package bad

func F() {
	go func() {}()
}
