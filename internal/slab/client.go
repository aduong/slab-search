package slab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a Slab API client
type Client struct {
	graphqlURL string
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a new Slab API client
func NewClient(token string) *Client {
	return &Client{
		graphqlURL: "https://slab.render.com/graphql",
		baseURL:    "https://slab.render.com",
		token:      token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// graphQLRequest represents a GraphQL request
type graphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

// graphQLResponse represents a GraphQL response
type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

// doGraphQL performs a GraphQL request
func (c *Client) doGraphQL(ctx context.Context, query string, variables map[string]interface{}, result interface{}) error {
	req := graphQLRequest{
		Query:     query,
		Variables: variables,
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.graphqlURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
	}

	if result != nil {
		if err := json.Unmarshal(gqlResp.Data, result); err != nil {
			return fmt.Errorf("unmarshal data: %w", err)
		}
	}

	return nil
}

// GetTopics fetches all topics via currentSession
func (c *Client) GetTopics(ctx context.Context) ([]Topic, error) {
	query := `
	{
		currentSession {
			organization {
				topics {
					id
					name
				}
			}
		}
	}
	`

	var result struct {
		CurrentSession struct {
			Organization struct {
				Topics []Topic `json:"topics"`
			} `json:"organization"`
		} `json:"currentSession"`
	}

	if err := c.doGraphQL(ctx, query, nil, &result); err != nil {
		return nil, fmt.Errorf("get topics: %w", err)
	}

	return result.CurrentSession.Organization.Topics, nil
}

// GetAllSlimPosts fetches all posts via currentSession
func (c *Client) GetAllSlimPosts(ctx context.Context) ([]SlimPost, error) {
	query := `
	{
		currentSession {
			organization {
				posts {
					id
					title
					publishedAt
					updatedAt
					archivedAt
					topics {
						id
					}
				}
			}
		}
	}
	`

	var result struct {
		CurrentSession struct {
			Organization struct {
				Posts []SlimPost `json:"posts"`
			} `json:"organization"`
		} `json:"currentSession"`
	}

	if err := c.doGraphQL(ctx, query, nil, &result); err != nil {
		return nil, fmt.Errorf("get all posts: %w", err)
	}

	return result.CurrentSession.Organization.Posts, nil
}

// GetTopicPosts fetches all posts for a given topic
func (c *Client) GetTopicPosts(ctx context.Context, topicID string) ([]SlimPost, error) {
	query := `
	query GetTopicPosts($topicId: ID!, $first: Int) {
		topic(id: $topicId) {
			posts(first: $first) {
				edges {
					node {
						id
						title
						publishedAt
						updatedAt
						archivedAt
						topics {
							id
							name
						}
					}
				}
			}
		}
	}
	`

	var result struct {
		Topic struct {
			Posts struct {
				Edges []struct {
					Node SlimPost `json:"node"`
				} `json:"edges"`
			} `json:"posts"`
		} `json:"topic"`
	}

	variables := map[string]interface{}{
		"topicId": topicID,
		"first":   100, // Fetch up to 100 posts per topic
	}

	if err := c.doGraphQL(ctx, query, variables, &result); err != nil {
		return nil, fmt.Errorf("get topic posts: %w", err)
	}

	var posts []SlimPost
	for _, edge := range result.Topic.Posts.Edges {
		posts = append(posts, edge.Node)
	}

	return posts, nil
}

// GetPost fetches full metadata for a single post
func (c *Client) GetPost(ctx context.Context, postID string) (*Post, error) {
	query := `
	query GetPost($id: ID!) {
		post(id: $id) {
			id
			title
			publishedAt
			updatedAt
			archivedAt
			owner {
				id
				name
				email
			}
			topics {
				id
				name
			}
		}
	}
	`

	var result struct {
		Post *Post `json:"post"`
	}

	variables := map[string]interface{}{
		"id": postID,
	}

	if err := c.doGraphQL(ctx, query, variables, &result); err != nil {
		return nil, fmt.Errorf("get post: %w", err)
	}

	return result.Post, nil
}

// GetMarkdown fetches the markdown content for a post
func (c *Client) GetMarkdown(ctx context.Context, postID string) (string, error) {
	url := fmt.Sprintf("%s/posts/%s/export/markdown", c.baseURL, postID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status: %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	return string(body), nil
}
