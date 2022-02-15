package installhtml

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	api "github.com/openshift/sippy/pkg/apis/api"
	"github.com/openshift/sippy/pkg/db"
	"k8s.io/klog"

	"github.com/openshift/sippy/pkg/testgridanalysis/testidentification"

	sippyprocessingv1 "github.com/openshift/sippy/pkg/apis/sippyprocessing/v1"
	"github.com/openshift/sippy/pkg/html/generichtml"
	"github.com/openshift/sippy/pkg/testgridanalysis/testreportconversion"
	"github.com/openshift/sippy/pkg/util"
	"github.com/openshift/sippy/pkg/util/sets"
)

type ResponseFormat string

const (
	HTML ResponseFormat = "html"
	JSON ResponseFormat = "json"
)

var individualInstallUpgradeColor = generichtml.ColorizationCriteria{
	MinRedPercent:    0,  // failure.  In this range, there is a systemic failure so severe that a reliable signal isn't available.
	MinYellowPercent: 90, // at risk.  In this range, there is a systemic problem that needs to be addressed.
	MinGreenPercent:  95, // no action required.
}

type currPrevTestResult struct {
	curr sippyprocessingv1.TestResult
	prev *sippyprocessingv1.TestResult
}

func (r currPrevTestResult) toTest(name string) api.Test {
	test := api.Test{
		Name:                  name,
		CurrentPassPercentage: r.curr.PassPercentage,
		CurrentSuccesses:      r.curr.Successes,
		CurrentFailures:       r.curr.Failures,
		CurrentFlakes:         r.curr.Flakes,
		CurrentRuns:           r.curr.Successes + r.curr.Failures + r.curr.Flakes,
	}

	if r.prev != nil {
		test.PreviousPassPercentage = r.prev.PassPercentage
		test.PreviousFlakes = r.prev.Flakes
		test.PreviousFailures = r.prev.Failures
		test.PreviousSuccesses = r.prev.Successes
		test.PreviousRuns = r.prev.Successes + r.prev.Failures + r.prev.Flakes
	}

	return test
}

func (c *currPrevFailedTestResult) toCurrPrevTestResult() *currPrevTestResult {
	if c == nil {
		return nil
	}
	if c.prev == nil {
		return &currPrevTestResult{curr: c.curr.TestResultAcrossAllJobs}
	}
	return &currPrevTestResult{
		curr: c.curr.TestResultAcrossAllJobs,
		prev: &c.prev.TestResultAcrossAllJobs,
	}
}

type currPrevFailedTestResult struct {
	curr sippyprocessingv1.FailingTestResult
	prev *sippyprocessingv1.FailingTestResult
}

type testsByVariant struct {
	aggregateResultByTestName      map[string]*currPrevFailedTestResult
	testNameToVariantToTestResult  map[string]map[string]*currPrevTestResult // these are the other rows in the table.
	aggregationToOverallTestResult map[string]*currPrevTestResult            // this is the first row of the table, summarizing all data.  If empty or nil, no summary is given.
}

func getDataForTestsByVariant(
	curr, prev sippyprocessingv1.TestReport,
	isInterestingTest testreportconversion.TestResultFilterFunc,
	isAggregateTest testreportconversion.TestResultFilterFunc,
) testsByVariant {
	ret := testsByVariant{
		aggregateResultByTestName:      map[string]*currPrevFailedTestResult{},
		testNameToVariantToTestResult:  map[string]map[string]*currPrevTestResult{},
		aggregationToOverallTestResult: map[string]*currPrevTestResult{},
	}

	for _, test := range curr.ByTest {
		if isInterestingTest(test.TestResultAcrossAllJobs) {
			ret.aggregateResultByTestName[test.TestName] = &currPrevFailedTestResult{curr: test}
			if prevTestResult := util.FindFailedTestResult(test.TestName, prev.ByTest); prevTestResult != nil {
				ret.aggregateResultByTestName[test.TestName].prev = prevTestResult
			}
		}
	}

	// now that we have the tests, let's run through all the variants to pull the variant aggregation for each of the tests in question
	for testName := range ret.aggregateResultByTestName {
		if _, ok := ret.testNameToVariantToTestResult[testName]; !ok {
			ret.testNameToVariantToTestResult[testName] = map[string]*currPrevTestResult{}
		}
		for _, variant := range curr.ByVariant {
			for _, test := range variant.AllTestResults {
				if test.Name != testName {
					continue
				}

				ret.testNameToVariantToTestResult[testName][variant.VariantName] = &currPrevTestResult{curr: test}
				if prevVariant := util.FindVariantResultsForName(variant.VariantName, prev.ByVariant); prevVariant != nil {
					if prevTestResult := util.FindTestResult(test.Name, prevVariant.AllTestResults); prevTestResult != nil {
						ret.testNameToVariantToTestResult[testName][variant.VariantName].prev = prevTestResult
					}
				}
				break
			}
		}
	}

	for _, variant := range curr.ByVariant {
		for _, test := range variant.AllTestResults {
			if isAggregateTest(test) {
				ret.aggregationToOverallTestResult[variant.VariantName] = &currPrevTestResult{curr: test}

				if prevVariant := util.FindVariantResultsForName(variant.VariantName, prev.ByVariant); prevVariant != nil {
					if prevTestResult := util.FindTestResult(test.Name, prevVariant.AllTestResults); prevTestResult != nil {
						ret.aggregationToOverallTestResult[variant.VariantName].prev = prevTestResult
					}
				}
				break
			}
		}
	}

	return ret
}

func getDataForTestsByVariantFromDB(
	db *db.DB,
	release string,
	testSubStrings []string,
	isAggregateTest testreportconversion.TestResultFilterFunc, // TODO: is this needed?
) (testsByVariant, error) {
	ret := testsByVariant{
		aggregateResultByTestName:      map[string]*currPrevFailedTestResult{}, // not used in output, maybe we can skip
		testNameToVariantToTestResult:  map[string]map[string]*currPrevTestResult{},
		aggregationToOverallTestResult: map[string]*currPrevTestResult{}, // may not be used in output in our first use case
	}

	now := time.Now()

	testSubstringFilter := strings.Join(testSubStrings, "|")

	// Query and group by variant:
	var testReports []api.Test
	q := `
WITH results AS (
    SELECT name,
           release,
           sum(current_runs)       AS current_runs,
           sum(current_successes)  AS current_successes,
           sum(current_failures)   AS current_failures,
           sum(current_flakes)     AS current_flakes,
           sum(previous_runs)      AS previous_runs,
           sum(previous_successes) AS previous_successes,
           sum(previous_failures)  AS previous_failures,
           sum(previous_flakes)    AS previous_flakes,
           unnest(variants)        AS variant
    FROM prow_test_report_7d_matview
	WHERE release = @release AND name ~* @testsubstrings
    GROUP BY name, release, variant
)
SELECT *,
       current_successes * 100.0 / NULLIF(current_runs, 0) AS current_pass_percentage,
       current_failures * 100.0 / NULLIF(current_runs, 0) AS current_failure_percentage,
       previous_successes * 100.0 / NULLIF(previous_runs, 0) AS previous_pass_percentage,
       previous_failures * 100.0 / NULLIF(previous_runs, 0) AS previous_failure_percentage,
       (current_successes * 100.0 / NULLIF(current_runs, 0)) - (previous_successes * 100.0 / NULLIF(previous_runs, 0)) AS net_improvement
FROM results;
`
	r := db.DB.Raw(q,
		sql.Named("release", release),
		sql.Named("testsubstrings", testSubstringFilter)).Scan(&testReports)
	if r.Error != nil {
		klog.Error(r.Error)
		return ret, r.Error
	}

	elapsed := time.Since(now)
	klog.Infof("BuildTestsResult completed in %s with %d results from db", elapsed, len(testReports))

	// We don't need to populate the ret.aggregateResultByTestName as it is only used in calculating the old way,
	// and not when we output below.
	// We *may* not need to populate ret.aggregationToOverallTestResult either, as this is only needed sometimes below.
	// TODO: ^^ when? who calls it this way?

	// We have a pretty clean list of TestResults by variant from the db, but transform to the old datastructure
	// to re-use the response writing logic below.
	for _, tr := range testReports {
		if _, ok := ret.testNameToVariantToTestResult[tr.Name]; !ok {
			ret.testNameToVariantToTestResult[tr.Name] = map[string]*currPrevTestResult{}
		}
		ret.testNameToVariantToTestResult[tr.Name][tr.Variant] = &currPrevTestResult{
			curr: sippyprocessingv1.TestResult{
				Name:              tr.Name,
				Successes:         tr.CurrentSuccesses,
				Failures:          tr.CurrentFailures,
				Flakes:            tr.CurrentFlakes,
				PassPercentage:    tr.CurrentPassPercentage,
				BugList:           nil, // TODO
				AssociatedBugList: nil, // TODO
			},
			prev: &sippyprocessingv1.TestResult{
				Name:              tr.Name,
				Successes:         tr.PreviousSuccesses,
				Failures:          tr.PreviousFailures,
				Flakes:            tr.PreviousFlakes,
				PassPercentage:    tr.PreviousPassPercentage,
				BugList:           nil, // TODO
				AssociatedBugList: nil, // TODO
			},
		}
	}
	return ret, nil

}

//nolint:goconst
func (a testsByVariant) getTableHTML(
	title string,
	anchor string,
	description string,
	aggregationNames []string, // these are the columns
	testNameToDisplayName func(string) string,
) string {
	// test name | bug | pass rate | higher/lower | pass rate
	s := fmt.Sprintf(`
	<table class="table">
		<tr>
			<th colspan=%d class="text-center">
				<a class="text-dark" id="%s" href="#%s">%s</a>
				<i class="fa fa-info-circle" title=%q></i>
			</th>
		</tr>
	`,
		len(aggregationNames)+2,
		anchor,
		anchor,
		title,
		description,
	)

	// print variant column headers
	s += "    <tr>"
	s += "      <td nowrap=\"nowrap\">Test Name</td>\n"
	for _, aggregationName := range aggregationNames {
		s += "      <th class=\"text-center\"><nobr>" + aggregationName + "</nobr></th>\n"
	}
	s += "		</tr>\n"

	// now the overall install results by variant
	if len(a.aggregationToOverallTestResult) > 0 {
		s += "    <tr>"
		s += "      <td>Overall</td>\n"
		for _, variantName := range aggregationNames {
			s += installCellHTMLFromTestResult(a.aggregationToOverallTestResult[variantName], generichtml.OverallInstallUpgradeColors)
		}
		s += "		</tr>"
	}

	// now the main results by operator, by variant
	for _, testName := range sets.StringKeySet(a.testNameToVariantToTestResult).List() {
		testDisplayName := testNameToDisplayName(testName)
		s += "    <tr>"
		s += "      <td class=\"\"><nobr>" + testDisplayName + "</nobr></td>\n"
		variantResults := a.testNameToVariantToTestResult[testName]
		for _, variantName := range aggregationNames {
			s += installCellHTMLFromTestResult(variantResults[variantName], individualInstallUpgradeColor)
		}
		s += "		</tr>"
	}

	s += "</table>"

	return s
}

//nolint:goconst
func (a testsByVariant) getTableJSON(
	title string,
	description string,
	aggregationNames []string, // these are the columns
	testNameToDisplayName func(string) string,
) string {
	summary := map[string]interface{}{
		"title":        title,
		"description":  description,
		"column_names": aggregationNames,
	}
	tests := make(map[string]map[string]api.Test)

	// now the overall install results by variant
	if len(a.aggregationToOverallTestResult) > 0 {
		results := make(map[string]api.Test)
		for _, variantName := range aggregationNames {
			data := a.aggregationToOverallTestResult[variantName].toTest(variantName)
			results[variantName] = data
		}
		tests["Overall"] = results
	}

	for _, testName := range sets.StringKeySet(a.testNameToVariantToTestResult).List() {
		testDisplayName := testNameToDisplayName(testName)
		variantResults := a.testNameToVariantToTestResult[testName]
		results := make(map[string]api.Test)
		for _, variantName := range aggregationNames {
			if data, ok := variantResults[variantName]; ok {
				results[variantName] = data.toTest(variantName)
			}
		}
		tests[testDisplayName] = results
	}

	summary["tests"] = tests
	result, err := json.Marshal(summary)
	if err != nil {
		panic(err)
	}

	return string(result)
}

func getOperatorFromTest(testName string) string {
	if ret := testidentification.GetOperatorNameFromTest(testName); len(ret) > 0 {
		return ret
	}
	return testName
}

func noChange(testName string) string {
	return testName
}

func installCellHTMLFromTestResult(cellResult *currPrevTestResult, colors generichtml.ColorizationCriteria) string {
	if cellResult == nil {
		return "      <td class=\"text-center table-secondary\"><nobr>no-data</nobr></td>"
	}

	// we filter out 100% passing results, so this almost certainly means we always pass.  We default to 100
	passPercentage := cellResult.curr.PassPercentage
	arrow := generichtml.GetArrowForTestResult(cellResult.curr, cellResult.prev)
	color := colors.GetColor(passPercentage, cellResult.curr.Successes+cellResult.curr.Failures+cellResult.curr.Flakes)
	if cellResult.prev == nil {
		return fmt.Sprintf("      <td class=\"text-center %v\"><nobr>%0.2f%% %v NA</nobr></td>", color, passPercentage, arrow)
	}

	return fmt.Sprintf("      <td class=\"text-center %v\"><nobr>%0.2f%% %v %0.2f%% </nobr></td>", color, passPercentage, arrow, cellResult.prev.PassPercentage)
}

type testFilterFunc func(testResult sippyprocessingv1.TestResult) bool

func failingTestsRows(topFailingTests, prevTests []sippyprocessingv1.FailingTestResult, release string, testFilterFn testFilterFunc) string {
	s := ""

	for _, testResult := range topFailingTests {
		if !testFilterFn(testResult.TestResultAcrossAllJobs) {
			continue
		}

		s +=
			generichtml.NewTestResultRendererForFailedTestResult("", testResult, release).
				WithPreviousFailedTestResult(util.FindFailedTestResult(testResult.TestName, prevTests)).
				ToHTML()
	}

	return s
}
