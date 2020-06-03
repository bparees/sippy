package html

import (
	"fmt"
	//"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"text/template"

	"k8s.io/klog"

	"github.com/bparees/sippy/pkg/util"
)

var (
	escapeRegex *regexp.Regexp = regexp.MustCompile(`\[.*?\]`)
)

const (
	up   = `<i class="fa fa-arrow-up" style="font-size:28px;color:green"></i>`
	down = `<i class="fa fa-arrow-down" style="font-size:28px;color:red"></i>`
	flat = `<i class="fa fa-arrows-h" style="font-size:28px;color:darkgray"></i>`

	htmlPageStart = `
<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8"><title>%s</title>
<link rel="stylesheet" href="https://stackpath.bootstrapcdn.com/bootstrap/4.1.3/css/bootstrap.min.css" integrity="sha384-MCw98/SFnGE8fJT3GXwEOngsV7Zt27NXFoaoApmYm81iuXoPkFOJwJ8ERdknLPMO" crossorigin="anonymous">
<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/4.7.0/css/font-awesome.min.css">
<meta name="viewport" content="width=device-width, initial-scale=1, shrink-to-fit=no">
<style>
@media (max-width: 992px) {
  .container {
    width: 100%%;
    max-width: none;
  }
}
</style>
</head>

<body>
<div class="container">
`

	htmlPageEnd = `
</div>
Data current as of: %s
</body>
</html>
`

	dashboardPageHtml = `
<link rel="stylesheet" href="https://stackpath.bootstrapcdn.com/bootstrap/4.1.3/css/bootstrap.min.css" integrity="sha384-MCw98/SFnGE8fJT3GXwEOngsV7Zt27NXFoaoApmYm81iuXoPkFOJwJ8ERdknLPMO" crossorigin="anonymous">
<style>
#table td, #table th {
	border: 
}
</style>

<h1 class=text-center>CI Release Health Summary</h1>

<p class="small mb-3">
	Jump to: <a href="#SummaryAcrossAllJobs">Summary Across All Jobs</a> | <a href="#FailureGroupings">Failure Groupings</a> | 
	         <a href="#JobPassRatesByPlatform">Job Pass Rates By Platform</a> | <a href="#TopFailingTests">Top Failing Tests</a> | 
	         <a href="#JobPassRatesByJobName">Job Pass Rates By Job Name</a> | <a href="#CanaryTestFailures">Canary Test Failures</a> |
	         <a href="#JobRunsWithFailureGroups">Job Runs With Failure Groups</a>
</p>

{{ summaryAcrossAllJobs .Current.All .Prev.All }}

{{ failureGroups .Current.FailureGroups .Prev.FailureGroups }}

{{ summaryJobsByPlatform .Current .Prev }}

{{ summaryTopFailingTests .Current.TopFailingTestsWithoutBug .Current.TopFailingTestsWithBug .Prev.All }}

{{ summaryJobPassRatesByJobName .Current .Prev }}

{{ canaryTestFailures .Current.All }}

{{ failureGroupList .Current }}
`
)

func summaryAcrossAllJobs(result, resultPrev map[string]util.SortedAggregateTestResult) string {

	all := result["all"]
	allPrev := resultPrev["all"]

	summary := `
	<table class="table">
		<tr>
			<th colspan=3 class="text-center"><a class="text-dark" id="SummaryAcrossAllJobs" href="#SummaryAcrossAllJobs">Summary Across All Jobs</a></th>			
		</tr>
		<tr>
			<th/><th>Latest 7 days</th><th>Previous 7 days</th>
		</tr>
		<tr>
			<td>Test executions: </td><td>%d</td><td>%d</td>
		</tr>
		<tr>
			<td>Test Pass Percentage: </td><td>%0.2f</td><td>%0.2f</td>
		</tr>
	</table>`
	s := fmt.Sprintf(summary, all.Successes+all.Failures, allPrev.Successes+allPrev.Failures, all.TestPassPercentage, allPrev.TestPassPercentage)
	return s
}

func failureGroups(failureGroups, failureGroupsPrev []util.JobRunResult) string {
	count, countPrev, median, medianPrev, avg, avgPrev := 0, 0, 0, 0, 0, 0
	for _, group := range failureGroups {
		count += group.TestFailures
	}
	for _, group := range failureGroupsPrev {
		countPrev += group.TestFailures
	}
	if len(failureGroups) != 0 {
		median = failureGroups[len(failureGroups)/2].TestFailures
		avg = count / len(failureGroups)
	}
	if len(failureGroupsPrev) != 0 {
		medianPrev = failureGroupsPrev[len(failureGroupsPrev)/2].TestFailures
		avgPrev = count / len(failureGroupsPrev)
	}

	groups := `
	<table class="table">
		<tr>
			<th colspan=3 class="text-center"><a class="text-dark" title="Statistics on how often we see a cluster of test failures in a single run.  Such clusters are indicative of cluster infrastructure problems that impact many tests and should be investigated.  See below for a link to specific jobs that show large clusters of test failures."  id="FailureGroupings" href="#FailureGroupings">Failure Groupings</a></th>
		</tr>
		<tr>
			<th/><th>Latest 7 days</th><th>Previous 7 days</th>
		</tr>
		<tr>
			<td>Job Runs with a Failure Group: </td><td>%d</td><td>%d</td>
		</tr>
		<tr>
			<td>Average Failure Group Size: </td><td>%d</td><td>%d</td>
		</tr>
		<tr>
			<td>Median Failure Group Size: </td><td>%d</td><td>%d</td>
		</tr>
	</table>`
	s := fmt.Sprintf(groups, len(failureGroups), len(failureGroupsPrev), avg, avgPrev, median, medianPrev)
	return s
}

func getPrevPlatform(platform string, jobsByPlatform []util.JobResult) *util.JobResult {
	for _, v := range jobsByPlatform {
		if v.Platform == platform {
			return &v
		}
	}
	return nil
}

func summaryJobsByPlatform(report, reportPrev util.TestReport) string {
	jobsByPlatform := util.SummarizeJobsByPlatform(report)
	jobsByPlatformPrev := util.SummarizeJobsByPlatform(reportPrev)

	s := `
	<table class="table">
		<tr>
			<th colspan=4 class="text-center"><a class="text-dark" title="Aggregation of all job runs for a given platform, sorted by passing rate percentage.  Platforms at the top of this list have unreliable CI jobs or the product is unreliable on those platforms." id="JobPassRatesByPlatform" href="#JobPassRatesByPlatform">Job Pass Rates By Platform</a></th>
		</tr>
		<tr>
			<th>Platform</th><th>Latest 7 days</th><th/><th>Previous 7 days</th>
		</tr>
	`
	template := `
		<tr>
			<td>%s</td><td>%0.2f%% (%d runs)</td><td>%s</td><td>%0.2f%% (%d runs)</td>
		</tr>
	`

	for _, v := range jobsByPlatform {
		prev := getPrevPlatform(v.Platform, jobsByPlatformPrev)
		p := util.Percent(v.Successes, v.Failures)
		if prev != nil {
			pprev := util.Percent(prev.Successes, prev.Failures)
			arrow := flat
			delta := 5.0
			if v.Successes+v.Failures > 80 {
				delta = 2
			}
			if p > pprev+delta {
				arrow = up
			} else if p < pprev-delta {
				arrow = down
			}
			s = s + fmt.Sprintf(template, v.Platform,
				p,
				v.Successes+v.Failures,
				arrow,
				pprev,
				prev.Successes+prev.Failures,
			)
		} else {
			s = s + fmt.Sprintf(template, v.Platform,
				p,
				v.Successes+v.Failures,
				"",
				-1, -1,
			)
		}
	}
	s = s + "</table>"
	return s
}

func getPrevTest(test string, testResults []util.TestResult) *util.TestResult {
	for _, v := range testResults {
		if v.Name == test {
			return &v
		}
	}
	return nil
}

func summaryTopFailingTests(topFailingTestsWithoutBug, topFailingTestsWithBug []*util.TestResult, resultPrev map[string]util.SortedAggregateTestResult) string {
	allPrev := resultPrev["all"]

	// test name | bug | pass rate | higher/lower | pass rate
	s := `
	<table class="table">
		<tr>
			<th colspan=5 class="text-center"><a class="text-dark" title="Most frequently failing tests without a known bug, sorted by passing rate.  The link will prepopulate a BZ template to be filled out and submitted to report a bug against the test." id="TopFailingTests" href="#TopFailingTests">Top Failing Tests Without A Bug</a></th>
		</tr>
		<tr>
			<th colspan=2/><th class="text-center">Latest 7 Days</th><th/><th class="text-center">Previous 7 Days</th>
		</tr>
		<tr>
			<th>Test Name</th><th>File a Bug</th><th>Pass Rate</th><th/><th>Pass Rate</th>
		</tr>
	`
	template := `
		<tr>
			<td>%s</td><td>%s</td><td>%0.2f%% (%d runs)</td><td>%s</td><td>%0.2f%% (%d runs)</td>
		</tr>
	`
	naTemplate := `
		<tr>
			<td>%s</td><td>%s</td><td>%0.2f%% (%d runs)</td><td/><td>NA</td>
		</tr>
	`

	for _, test := range topFailingTestsWithoutBug {
		encodedTestName := url.QueryEscape(regexp.QuoteMeta(test.Name))

		testLink := fmt.Sprintf("<a target=\"_blank\" href=\"https://search.svc.ci.openshift.org/?maxAge=168h&context=1&type=bug%%2Bjunit&name=&maxMatches=5&maxBytes=20971520&groupBy=job&search=%s\">%s</a>", encodedTestName, test.Name)
		testPrev := getPrevTest(test.Name, allPrev.TestResults)

		bug := ""
		if test.BugErr != nil {
			bug = "Search Failed"
		} else {
			searchUrl := fmt.Sprintf("https://search.svc.ci.openshift.org/?maxAge=168h&context=1&type=bug%%2Bjunit&name=&maxMatches=5&maxBytes=20971520&groupBy=job&search=%s", encodedTestName)
			exampleJob := "FIXME: Replace this paragraph with a particular job URI from the search results to ground discussion.  A given test may fail for several reasons, and this bug should be scoped to one of those reasons.  Ideally you'd pick a job showing the most-common reason, but since that's hard to determine, you may also chose to pick a job at random.  Release-gating jobs (release-openshift-...) should be preferred over presubmits (pull-ci-...) because they are closer to the released product and less likely to have in-flight code changes that complicate analysis."
			bug = fmt.Sprintf("<a href=https://bugzilla.redhat.com/enter_bug.cgi?classification=Red%%20Hat&product=OpenShift%%20Container%%20Platform&cf_internal_whiteboard=buildcop&short_desc=%[1]s&cf_environment=%[1]s&comment=test:%%0A%[1]s%%20%%0A%%0Ais%%20failing%%20frequently%%20in%%20CI,%%20see%%20search%%20results:%%0A%s%%OA%%OA%s>Open a bug</a>", url.QueryEscape(test.Name), url.QueryEscape(searchUrl), url.QueryEscape(exampleJob))
		}

		if testPrev != nil {
			arrow := flat
			delta := 5.0
			if test.Successes+test.Failures > 80 {
				delta = 2
			}
			if test.PassPercentage > testPrev.PassPercentage+delta {
				arrow = up
			} else if test.PassPercentage < testPrev.PassPercentage-delta {
				arrow = down
			}
			s += fmt.Sprintf(template, testLink, bug, test.PassPercentage, test.Successes+test.Failures, arrow, testPrev.PassPercentage, testPrev.Successes+testPrev.Failures)
		} else {
			s += fmt.Sprintf(naTemplate, testLink, bug, test.PassPercentage, test.Successes+test.Failures)
		}
	}

	s += `<tr>
			<th colspan=5 class="text-center"><a class="text-dark" title="Most frequently failing tests with a known bug, sorted by passing rate.">Top Failing Tests With A Bug</a></th>
		  </tr>
		<tr>
			<th>Test Name</th><th>BZ</th><th>Pass Rate</th><th/><th>Pass Rate</th>
		</tr>`

	for _, test := range topFailingTestsWithBug {
		encodedTestName := url.QueryEscape(regexp.QuoteMeta(test.Name))

		testLink := fmt.Sprintf("<a target=\"_blank\" href=\"https://search.svc.ci.openshift.org/?maxAge=168h&context=1&type=bug%%2Bjunit&name=&maxMatches=5&maxBytes=20971520&groupBy=job&search=%s\">%s</a>", encodedTestName, test.Name)
		testPrev := getPrevTest(test.Name, allPrev.TestResults)

		klog.V(2).Infof("processing top failing tests with bug %s, bugs: %v", test.Name, test.BugList)
		bug := ""
		for _, b := range test.BugList {
			bugID := strings.TrimPrefix(b, "https://bugzilla.redhat.com/show_bug.cgi?id=")
			bug += fmt.Sprintf("<a href=%s>%s</a> ", b, bugID)
		}
		if testPrev != nil {
			arrow := flat
			delta := 5.0
			if test.Successes+test.Failures > 80 {
				delta = 2
			}
			if test.PassPercentage > testPrev.PassPercentage+delta {
				arrow = up
			} else if test.PassPercentage < testPrev.PassPercentage-delta {
				arrow = down
			}
			s += fmt.Sprintf(template, testLink, bug, test.PassPercentage, test.Successes+test.Failures, arrow, testPrev.PassPercentage, testPrev.Successes+testPrev.Failures)
		} else {
			s += fmt.Sprintf(naTemplate, testLink, bug, test.PassPercentage, test.Successes+test.Failures)
		}
	}

	s = s + "</table>"
	return s
}

func getPrevJob(job string, jobRunsByJob []util.JobResult) *util.JobResult {
	for _, v := range jobRunsByJob {
		if v.Name == job {
			return &v
		}
	}
	return nil
}

func summaryJobPassRatesByJobName(report, reportPrev util.TestReport) string {
	jobRunsByName := util.SummarizeJobsByName(report)
	jobRunsByNamePrev := util.SummarizeJobsByName(reportPrev)

	s := `
	<table class="table">
		<tr>
			<th colspan=4 class="text-center"><a class="text-dark" title="Passing rate for each job definition, sorted by passing percentage.  Jobs at the top of this list are unreliable or represent environments where the product is not stable and should be investigated." id="JobPassRatesByJobName" href="#JobPassRatesByJobName">Job Pass Rates By Job Name</a></th>
		</tr>
		<tr>
			<th>Name</th><th>Latest 7 days</th><th/><th>Previous 7 days</th>
		</tr>
	`
	template := `
		<tr>
			<td><a target="_blank" href="%s">%s</a></td><td>%0.2f%% (%d runs)</td><td>%s</td><td>%0.2f%% (%d runs)</td>
		</tr>
	`

	naTemplate := `
		<tr>
			<td><a target="_blank" href="%s">%s</a></td><td>%0.2f%% (%d runs)</td><td/><td>NA</td>
		</tr>
	`

	for _, v := range jobRunsByName {
		prev := getPrevJob(v.Name, jobRunsByNamePrev)
		p := util.Percent(v.Successes, v.Failures)
		if prev != nil {
			pprev := util.Percent(prev.Successes, prev.Failures)
			arrow := flat
			delta := 5.0
			if v.Successes+v.Failures > 80 {
				delta = 2
			}
			if v.PassPercentage > prev.PassPercentage+delta {
				arrow = up
			} else if v.PassPercentage < prev.PassPercentage-delta {
				arrow = down
			}
			s = s + fmt.Sprintf(template, v.TestGridUrl, v.Name,
				p,
				v.Successes+v.Failures,
				arrow,
				pprev,
				prev.Successes+prev.Failures,
			)
		} else {
			s = s + fmt.Sprintf(naTemplate, v.TestGridUrl, v.Name,
				p,
				v.Successes+v.Failures,
			)
		}
	}
	s = s + "</table>"
	return s
}

func canaryTestFailures(result map[string]util.SortedAggregateTestResult) string {
	all := result["all"].TestResults

	// test name | bug | pass rate | higher/lower | pass rate
	s := `
	<table class="table">
		<tr>
			<th colspan=2 class="text-center"><a class="text-dark" title="Tests which historically pass but failed in a job run.  Job run should be investigated because these historically stable tests were probably disrupted by a major cluster bug." id="CanaryTestFailures" href="#CanaryTestFailures">Canary Test Failures</a></th>
		</tr>
		<tr>
			<th>Test Name</th><th>Pass Rate</th>
		</tr>
	`
	template := `
		<tr>
			<td>%s</td><td>%0.2f%% (%d runs)</td>
		</tr>
	`

	for i := len(all) - 1; i > len(all)-10; i-- {
		test := all[i]
		encodedTestName := url.QueryEscape(regexp.QuoteMeta(test.Name))

		testLink := fmt.Sprintf("<a target=\"_blank\" href=\"https://search.svc.ci.openshift.org/?maxAge=168h&context=1&type=bug%%2Bjunit&name=&maxMatches=5&maxBytes=20971520&groupBy=job&search=%s\">%s</a>", encodedTestName, test.Name)

		s += fmt.Sprintf(template, testLink, test.PassPercentage, test.Successes+test.Failures)
	}
	s = s + "</table>"
	return s
}
func failureGroupList(report util.TestReport) string {
	s := `
	<table class="table">
		<tr>
			<th colspan=2 class="text-center"><a class="text-dark" title="Job runs where a large number of tests failed.  This is usually indicative of a cluster infrastructure problem, not a test issue, and should be investigated as such." id="JobRunsWithFailureGroups" href="#JobRunsWithFailureGroups">Job Runs With Failure Groups</a></th>
		</tr>
		<tr>
			<th>Job</th><th>Failed Test Count</th>
		</tr>
	`

	template := `
	<tr>
		<td><a href=%s>%s</a></td><td>%d</td>
	</tr>`
	for _, fg := range report.FailureGroups {
		s += fmt.Sprintf(template, fg.Url, fg.Job, fg.TestFailures)
	}
	s = s + "</table>"
	return s
}

type TestReports struct {
	Current util.TestReport
	Prev    util.TestReport
}

func PrintHtmlReport(w http.ResponseWriter, req *http.Request, report, prevReport util.TestReport) {

	w.Header().Set("Content-Type", "text/html;charset=UTF-8")
	fmt.Fprintf(w, htmlPageStart, "Release CI Health Dashboard")

	var dashboardPage = template.Must(template.New("dashboardPage").Funcs(
		template.FuncMap{
			"summaryAcrossAllJobs":         summaryAcrossAllJobs,
			"failureGroups":                failureGroups,
			"summaryJobsByPlatform":        summaryJobsByPlatform,
			"summaryTopFailingTests":       summaryTopFailingTests,
			"summaryJobPassRatesByJobName": summaryJobPassRatesByJobName,
			"canaryTestFailures":           canaryTestFailures,
			"failureGroupList":             failureGroupList,
		},
	).Parse(dashboardPageHtml))

	if err := dashboardPage.Execute(w, TestReports{report, prevReport}); err != nil {
		klog.Errorf("Unable to render page: %v", err)
	}

	//w.Write(result)
	fmt.Fprintf(w, htmlPageEnd, report.Timestamp.Format("Jan 2 15:04 2006 MST"))
}
