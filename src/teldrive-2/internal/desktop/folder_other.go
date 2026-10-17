//go:build !windows

package desktop

// ChooseFolder reports that nobody can be asked for a folder here.
func ChooseFolder(string, string) (string, bool) { return "", false }
