package slack

import "testing"

func TestToMrkdwn(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			"markdown link becomes slack link",
			"See [Sales Dashboard](https://mb.example/dash/1) for details.",
			"See <https://mb.example/dash/1|Sales Dashboard> for details.",
		},
		{
			"double asterisk collapses to single",
			"Revenue was **Rp 66,22 Juta** last week.",
			"Revenue was *Rp 66,22 Juta* last week.",
		},
		{
			"heading becomes bold line",
			"## Weekly summary\nRevenue is up.",
			"*Weekly summary*\nRevenue is up.",
		},
		{
			"bold wrapping a link",
			"**[Dashboard](https://mb.example/d/2)**",
			"*<https://mb.example/d/2|Dashboard>*",
		},
		{
			"plain text untouched",
			"Revenue is up 12% week over week.",
			"Revenue is up 12% week over week.",
		},
		{
			"bare url untouched",
			"https://mb.example/d/3",
			"https://mb.example/d/3",
		},
		{
			"list markers survive",
			"- first\n- second",
			"- first\n- second",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ToMrkdwn(tc.in); got != tc.want {
				t.Fatalf("ToMrkdwn(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
