package sys

func CheckDockerAccessible() bool {
	return DockerAccessError() == nil
}

func CheckContainerRuntimeAccessible(command string) bool {
	return ContainerRuntimeAccessError(command) == nil
}

func extractContainerIDFromCgroup() string {
	return extractContainerIDFromCgroupFiles(defaultCgroupPaths)
}
