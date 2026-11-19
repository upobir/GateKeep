package startup

type errorString string

func (e errorString) Error() string {
	return string(e)
}
