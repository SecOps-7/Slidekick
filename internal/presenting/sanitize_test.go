package presenting

import "testing"

func TestSanitizeRemovesMetaNarration(t *testing.T) {
	cases := []struct{ in, want string }{
		// What the AI wrote in testing.
		{"Here is the second slide. The first point builds in now. And finally the second point.", ""},
		{"Here is the second slide. Revenue grew 12% this quarter.", "Revenue grew 12% this quarter."},
		{"The first point builds in now. Support tickets dropped by a third.", "Support tickets dropped by a third."},
		{"And finally the second point. Margins reached 41%.", "Margins reached 41%."},
		// Slides, numbers, the screen.
		{"On slide 3 we saw the plan. The plan cut costs by 8%.", "The plan cut costs by 8%."},
		{"This slide shows our revenue. Revenue doubled in two years.", "Revenue doubled in two years."},
		{"As you can see on screen, churn halved.", ""},
		{"As you can see, churn halved since March.", ""},
		{"Churn halved, as shown on the screen.", ""},
		// Navigation and transitions.
		{"Let's move on to the next slide.", ""},
		{"Moving on, the team grew to 40 people.", "The team grew to 40 people."},
		{"Next, hiring doubled.", "Hiring doubled."},
		{"Now let's go to the next topic.", ""},
		{"That brings us to the final part.", ""},
		{"Let's go back to the previous chart.", ""},
		{"Next up: pricing.", ""},
		// Builds, bullets, animations, clicks.
		{"The chart fades in.", ""},
		{"The next bullet appears now.", ""},
		{"Each bullet point is a risk we track.", ""},
		{"With the next click, the forecast appears.", ""},
		{"I'll click to reveal the total.", ""},
		{"The animation plays here.", ""},
		// Stage directions.
		{"[pause] Revenue grew 12%. (click) Costs fell.", "Revenue grew 12%. Costs fell."},
		{"*pauses* Three markets carry the growth.", "Three markets carry the growth."},
		{"Here is the chart.", ""},
		{"Next.", ""},
		// Paragraphs stay apart.
		{"Revenue grew.\n\nNext slide please.\n\nCosts fell.", "Revenue grew.\n\nCosts fell."},
	}
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("Sanitize(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeKeepsContent(t *testing.T) {
	keep := []string{
		"Sales moved up 12% in the second quarter.",
		"Next quarter we expect revenue to reach 5 million dollars.",
		"The next generation of chips uses 30% less power.",
		"Demand fades in winter and recovers by April.",
		"Click-through rate rose from 2.1% to 3.4%.",
		"The energy transition will take two decades.",
		"Here is what the data says: churn halved.",
		"Let's look at what drove the growth.",
		"Finally, costs fell by 8%.",
		"Then revenue rose.",
		"My second point is that support costs fell 20% after the launch.",
		"To start, revenue grew 12%.",
		"The risk appears next year, when the loans reset.",
		"Prices slid 4% after the announcement.",
		"E.g. the U.S. market grew 3.5% vs. 2.1% in Europe.",
		"Dr. Smith's team shipped 14 releases.",
		"Visitors who see the demo convert twice as often.",
	}
	for _, s := range keep {
		if got := Sanitize(s); got != s {
			t.Errorf("Sanitize(%q) = %q, want it unchanged", s, got)
		}
	}
}

func TestSanitizeIdempotent(t *testing.T) {
	for _, s := range []string{"Now, next, revenue grew 12%.", "Okay, so now: the margin is 41%. [beat] Here's the next one."} {
		once := Sanitize(s)
		if twice := Sanitize(once); twice != once {
			t.Errorf("Sanitize not idempotent on %q: %q then %q", s, once, twice)
		}
	}
}

func TestSentences(t *testing.T) {
	got := sentences(`It cost $4.5M. "Worth it!" she said. J. Smith agreed? Yes`)
	want := []string{"It cost $4.5M.", ` "Worth it!"`, " she said.", " J. Smith agreed?", " Yes"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}
