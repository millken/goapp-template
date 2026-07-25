package scaffold

import "testing"

func TestNewSpec(t *testing.T) {
	cases := []struct {
		in   string
		want Spec
	}{
		{
			in: "post",
			want: Spec{
				Resource: "post", Type: "Post", Receiver: "p",
				Package: "post", Table: "post", Route: "post",
				FileBase: "post", ViewDir: "post",
			},
		},
		{
			in: "blog-post",
			want: Spec{
				Resource: "blog-post", Type: "BlogPost", Receiver: "b",
				Package: "blogpost", Table: "blog_post", Route: "blog-post",
				FileBase: "blog_post", ViewDir: "blog-post",
			},
		},
		{
			in: "BlogPost",
			want: Spec{
				Resource: "BlogPost", Type: "BlogPost", Receiver: "b",
				Package: "blogpost", Table: "blog_post", Route: "blog-post",
				FileBase: "blog_post", ViewDir: "blog-post",
			},
		},
		{
			in: "user_account",
			want: Spec{
				Resource: "user_account", Type: "UserAccount", Receiver: "u",
				Package: "useraccount", Table: "user_account", Route: "user-account",
				FileBase: "user_account", ViewDir: "user-account",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := NewSpec(c.in)
			if err != nil {
				t.Fatalf("NewSpec: %v", err)
			}
			if got != c.want {
				t.Errorf("NewSpec(%q):\n got  %+v\n want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestNewSpec_Invalid(t *testing.T) {
	cases := []string{"", "post!", "naïve", "a.b"}
	for _, in := range cases {
		if _, err := NewSpec(in); err == nil {
			t.Errorf("expected error for %q, got nil", in)
		}
	}
}

// testModule is the module path the generator threads into generated imports.
// Tests that compile the output (build_test.go) must use the real module, since
// the generated code imports <module>/internal/app.
const testModule = "github.com/millken/goapp-template"
