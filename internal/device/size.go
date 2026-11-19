package device

import "fmt"

// FormatBytes writes a size the way the Finder does, in powers of ten: "1.9 GB", "340 MB", "0 KB".
// A size under a kilobyte is rounded up to one, since a folder is never really empty.
func FormatBytes(n int64) string {
	const kilo, mega, giga, tera = 1_000, 1_000_000, 1_000_000_000, 1_000_000_000_000
	if n <= 0 {
		return "0 KB"
	}
	if kb := (n + kilo - 1) / kilo; kb < 1000 {
		return fmt.Sprintf("%d KB", kb)
	}
	if mb := (n + mega/2) / mega; mb < 1000 {
		return fmt.Sprintf("%d MB", mb)
	}
	// Past a hundred, a decimal only widens the column.
	if gb := float64(n) / giga; gb < 99.95 {
		return fmt.Sprintf("%.1f GB", gb)
	} else if n < tera-giga/2 {
		return fmt.Sprintf("%.0f GB", gb)
	}
	return fmt.Sprintf("%.1f TB", float64(n)/tera)
}
