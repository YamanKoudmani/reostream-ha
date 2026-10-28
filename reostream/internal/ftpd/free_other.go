//go:build !unix

package ftpd

func freeBytes(string) (int64, bool) { return 0, false }
