package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/TofuOverdose/WebMapMaker/internal/linkcrawler"
	"github.com/TofuOverdose/WebMapMaker/internal/sitemap"
	"github.com/TofuOverdose/WebMapMaker/internal/utils/gost"
)

type InputData struct {
	TargetURL    string
	OutputPath   string
	OutputType   string
	SearchConfig linkcrawler.SearchConfig
	LogWriter    io.WriteCloser
}

func main() {
	inputData, err := getInputData()
	if err != nil {
		fmt.Println(err)
		return
	}

	cr := linkcrawler.NewLinkCrawler(inputData.SearchConfig, 0)

	defer inputData.LogWriter.Close()

	resChan, err := cr.GetInnerLinks(inputData.TargetURL)
	if err != nil {
		fmt.Println(err)
		return
	}

	results := make([]linkcrawler.SearchResult, 0)
	maxHops := 0

	// Configuring CLI
	type linksDisplayStats struct {
		TotalFoundCount int
		AcceptedCount   int
		FailedCount     int
	}

	linkStats := linksDisplayStats{
		TotalFoundCount: 0,
		AcceptedCount:   0,
		FailedCount:     0,
	}
	sdt := "\t[ {{.AcceptedCount}} accepted | {{.FailedCount}} errors | {{.TotalFoundCount}} total links found ]"

	statsDisplay, err := gost.NewDisplay(sdt, linkStats)
	if err != nil {
		panic(err)
	}

	pb := gost.NewBouncer(10, gost.BouncerCharSet{
		Inactive:    '░',
		Active:      '█',
		BorderLeft:  "|",
		BorderRight: "|",
		Separator:   "|",
	})

	tr := time.Millisecond * 50

	timer := gost.NewTimer(
		gost.TimerOptionShowUnit(true),
		gost.TimerOptionTimeFormatter(gost.TimeFormatterAdaptive),
		gost.TimerOptionSetDecoration(" (time elapsed: ", ") "),
	)

	statusBar := gost.NewStatusBar(tr, pb, statsDisplay, timer)
	statusBar.Run()
	statusBar.Write([]byte("Started crawling the website"))
	for res := range resChan {
		linkStats.TotalFoundCount++
		if res.Error != nil {
			linkStats.FailedCount++
			msg := fmt.Sprintf("FAIL %s: %s", res.Url, res.Error.Error())
			//inputData.LogWriter.Write([]byte(msg))
			_, err := statusBar.Write([]byte(msg))
			if err != nil {
				panic(err)
			}
		} else {
			linkStats.AcceptedCount++
			results = append(results, res)
			if res.Hops > maxHops {
				maxHops = res.Hops
			}
		}

		// Update display data
		statsDisplay.SetData(linkStats)
	}
	statusBar.Close()
	statusBar.Write([]byte("Finished crawling. Building sitemap..."))
	us := sitemap.NewUrlSet()

	for _, res := range results {
		priority := 1.0
		if res.Hops > 0 {
			priority = float64(res.Hops) / priority
		}
		us.AddUrl(*sitemap.NewUrl(res.Url, "", "", priority))
	}
	// Open output file
	f, err := os.Create(inputData.OutputPath)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()

	switch inputData.OutputType {
	case "XML":
		err = us.WriteXml(f)
	case "TXT":
		err = us.WritePlain(f)
	}
	if err != nil {
		msg := fmt.Sprintf("FATAL: %s\n", err.Error())
		inputData.LogWriter.Write([]byte(msg))
		return
	}
	statusBar.Write([]byte(fmt.Sprintf("Sitemap saved to %s", inputData.OutputPath)))
}

func getInputData() (*InputData, error) {
	inputData := InputData{}

	// First define the flags
	pTargetURL := flag.String("t", "", "Target URL to start crawling from")
	pOutputPath := flag.String("o", "", "Output file (either .txt or .xml)")
	pLogFile := flag.String("log", "", "Path to log file")
	// Then run the parser
	flag.Parse()
	// Validation for the received flags
	if err := validateURL(*pTargetURL); err != nil {
		return nil, err
	}
	inputData.TargetURL = *pTargetURL

	if ot, err := checkOutputFile(*pOutputPath, []string{"XML", "TXT"}); err != nil {
		return nil, err
	} else {
		inputData.OutputPath = *pOutputPath
		inputData.OutputType = ot
	}

	if wc, err := getWriteCloser(*pLogFile); err != nil {
		return nil, err
	} else {
		inputData.LogWriter = wc
	}

	// Set up the config object based on the received flags
	inputData.SearchConfig = linkcrawler.SearchConfig{
		IgnoreTopLevelDomain:  *flag.Bool("ignoreTopLevelDomain", true, "Set FALSE to include links with different top level domains (e.g. website.foo and website.bar)"),
		IncludeLinksWithQuery: *flag.Bool("includeWithQuery", false, "Set TRUE to include links with queries"),
		IncludeSubdomains:     *flag.Bool("includeSubdomains", false, "Set TRUE to include links to subdomains of the target URL"),
	}

	return &inputData, nil
}

func getWriteCloser(path string) (io.WriteCloser, error) {
	if path == "" {
		return os.Stdout, nil
	}

	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	return f, nil
}

func checkOutputFile(path string, allowedTypes []string) (string, error) {
	// Uppercase allowedTypes for convenience
	types := make([]string, len(allowedTypes))
	for i, t := range allowedTypes {
		types[i] = strings.ToUpper(t)
	}

	errTypes := fmt.Errorf("Output file type must be one of these: %s", strings.Join(types, ", "))

	fExt := strings.ToUpper(getExtension(path))
	if fExt == "" {
		return "", errTypes
	}
	// Check if the file extension is among allowed
	found := false
	for _, t := range types {
		if fExt == t {
			found = true
			break
		}
	}
	if !found {
		return "", errTypes
	}

	return fExt, nil
}

func getExtension(path string) string {
	parts := strings.Split(path, ".")
	return parts[len(parts)-1]
}

func validateURL(urlString string) error {
	errs := make([]string, 0)

	u, err := url.Parse(urlString)
	if err != nil {
		return fmt.Errorf("Invalid target URL: %s", err.Error())
	}

	if u.Scheme == "" {
		errs = append(errs, "scheme (http/https) is required")
	}

	if u.Host == "" {
		errs = append(errs, "hostname is required")
	}

	if len(errs) > 0 {
		return fmt.Errorf("Invalid target URL: %s", strings.Join(errs, ", "))
	}

	return nil
}
