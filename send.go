package disguise

import (
	"bufio"
	"fmt"
	"github.com/Disconnect24/appengine-smtp"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/log"
	"net/http"
	"regexp"
	"strings"
)

var mailFormName = regexp.MustCompile(`m\d+`)
var mailFrom = regexp.MustCompile(`^MAIL FROM:\s(w[0-9]*)@(?:.*)$`)
var rcptFrom = regexp.MustCompile(`^RCPT TO:\s(.*)@(.*)$`)

// Send takes POSTed mail by the Wii and stores it in the database for future usage.
func Send(w http.ResponseWriter, r *http.Request, global Config) {
	ctx := appengine.NewContext(r)

	w.Header().Add("Content-Type", "text/plain;charset=utf-8")

	// Create maps for storage of mail.
	mailPart := make(map[string]string)

	// Parse form in preparation for finding mail.
	err := r.ParseMultipartForm(1337)
	if err != nil {
		log.Errorf(ctx, "Unable to parse form: %v", err)
	}

	for name, contents := range r.MultipartForm.Value {
		if mailFormName.MatchString(name) {
			mailPart[name] = contents[0]
		}
	}

	eventualOutput := GenNormalErrorCode(ctx, 100, "Success.")
	eventualOutput += fmt.Sprint("mlnum=", len(mailPart), "\n")

	// Handle the all mail! \o/
	for mailNumber, contents := range mailPart {
		var linesToRemove string
		var wiiRecipientIDs []string
		var pcRecipientIDs []string
		var senderID string
		var data string

		// For every new line, handle as needed.
		scanner := bufio.NewScanner(strings.NewReader(contents))
		for scanner.Scan() {
			line := scanner.Text()
			// Add it to this mail's overall data.
			data += fmt.Sprintln(line)

			if line == "DATA" {
				// We don't actually need to do anything here,
				// just carry on.
				linesToRemove += fmt.Sprintln(line)
				continue
			}

			potentialMailFromWrapper := mailFrom.FindStringSubmatch(line)
			if potentialMailFromWrapper != nil {
				potentialMailFrom := potentialMailFromWrapper[1]
				if potentialMailFrom == "w9999999999990000" {
					eventualOutput += GenMailErrorCode(ctx, mailNumber, 351, "w9999999999990000 tried to send mail.")
					break
				}
				senderID = potentialMailFrom
				linesToRemove += fmt.Sprintln(line)
				continue
			}

			// -1 signifies all matches
			potentialRecipientWrapper := rcptFrom.FindAllStringSubmatch(line, -1)
			if potentialRecipientWrapper != nil {
				// We only need to work with the first match, which should be all we need.
				potentialRecipient := potentialRecipientWrapper[0]

				// layout:
				// potentialRecipient[0] = original matched string w/o groups
				// potentialRecipient[1] = w<16 digit ID>
				// potentialRecipient[2] = domain being sent to
				if potentialRecipient[2] == "wii.com" {
					// We're not gonna allow you to send to a defunct domain. ;P
					break
				} else if potentialRecipient[2] == global.Domain {
					// Wii <-> Wii mail. We can handle this.
					wiiRecipientIDs = append(wiiRecipientIDs, potentialRecipient[1])
				} else {
					// PC <-> Wii mail. We can't handle this, but SendGrid can.
					email := fmt.Sprintf("%s@%s", potentialRecipient[1], potentialRecipient[2])
					pcRecipientIDs = append(pcRecipientIDs, email)
				}

				linesToRemove += fmt.Sprintln(line)
			}
		}
		if err := scanner.Err(); err != nil {
			eventualOutput += GenMailErrorCode(ctx, mailNumber, 351, "Issue iterating over strings.")
			return
		}
		mailContents := strings.Replace(data, linesToRemove, "", -1)

		// We're done figuring out the mail, now it's time to act as needed.
		// For Wii recipients, we can just insert into the database.
		mailKey := datastore.NewIncompleteKey(ctx, "Mail", nil)
		for _, wiiRecipient := range wiiRecipientIDs {
			// We use a slice to cut off the `w` from the names.
			mailStruct := Mail{
				SenderID:    senderID[1:],
				Body:        mailContents,
				RecipientID: wiiRecipient[1:],
				Delivered:   false,
			}
			_, err := datastore.Put(ctx, mailKey, &mailStruct)
			if err != nil {
				eventualOutput += GenMailErrorCode(ctx, mailNumber, 450, "Database error.")
				return
			}
		}

		var pcError error
		for _, pcRecipient := range pcRecipientIDs {
			// Connect to the remote SMTP server.
			host := "smtp.sendgrid.net"
			auth := smtp.PlainAuth(
				"",
				"apikey",
				global.SendGridAPIKey,
				host,
			)
			// The only reason we can get away with the following is
			// because the Wii POSTs valid SMTP syntax.
			pcError = smtp.SendMail(
				ctx,
				fmt.Sprint(host, ":2525"),
				auth,
				fmt.Sprintf("%s@%s", senderID, global.Domain),
				[]string{pcRecipient},
				[]byte(mailContents),
			)
			if pcError != nil {
				log.Errorf(ctx, "Unable to send email: %v", pcError)
				// Escape loop, error writing is later on
				break
			}
		}

		if pcError != nil {
			eventualOutput += GenMailErrorCode(ctx, mailNumber, 351, "Issue sending mail via SendGrid.")
		} else {
			eventualOutput += GenMailErrorCode(ctx, mailNumber, 100, "Success.")
		}
	}

	// We're completely done now.
	fmt.Fprint(w, eventualOutput)
}
