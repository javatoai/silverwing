package genbuapi

import "context"

// ChooseNamespace exposes the same service-to-namespace fallback rule used by
// QueryServiceConfig.
func ChooseNamespace(service, environment string, namespaces []string) (string, error) {
	return chooseNamespace(service, environment, namespaces)
}

// ExtractLatestConfigContents exposes the shared ConfigMap snapshot parser.
func ExtractLatestConfigContents(response any) ([]string, string, error) {
	return extractLatestConfigContents(response)
}

// FindVersionRequest is the typed input for a direct Genbu version lookup.
type FindVersionRequest struct {
	Service string
	Tag     string
	AppID   string
}

// FindVersion resolves one version with the client's explicit token context.
func (client *Client) FindVersion(ctx context.Context, request FindVersionRequest) (Version, error) {
	ctx, cancel := context.WithTimeout(ctx, client.config.Timeout)
	defer cancel()
	token, _, err := client.tokenFor(ctx)
	if err != nil {
		return Version{}, err
	}
	return client.worker(ctx).findVersion(request.Service, request.Tag, token, request.AppID)
}
