package httputil

func TLSTrustEnvKeys() []string {
	return append([]string(nil), tlsTrustEnvKeys...)
}
