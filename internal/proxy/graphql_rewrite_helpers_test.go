package proxy

func InjectGuardFields(body []byte, toolName string) []byte {
	return injectGuardFields(body, toolName, &graphQLParseCache{})
}

func injectPRGuardFields(query string) string {
	return injectPRGuardFieldsParsed(query, &graphQLParseCache{})
}
