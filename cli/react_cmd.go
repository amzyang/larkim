package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// reactForm is how one emoji reaches a message. Feishu takes most of them as a
// reaction; the other two forms are what is left for the ones it refuses.
type reactForm string

const (
	// formReaction puts the emoji on the message, which is what the client's
	// own panel does.
	formReaction reactForm = "reaction"
	// formEmotion replies with the emoji inside a message, which is how
	// another tenant's culture emoji still reaches the other side as itself.
	formEmotion reactForm = "emotion"
	// formPicture replies with the picture the client draws the emoji as,
	// which is all that is left of one the client has withdrawn.
	formPicture reactForm = "picture"
)

// formFor picks how an emoji reaches a message. An emoji this build has no
// entry for is taken as a reaction: Feishu keeps the list and answers 231001
// for anything outside it, which says more than a guess made here would.
func formFor(key string) reactForm {
	e, known := emoji.ByKey(key)
	switch {
	case !known || e.Reactable():
		return formReaction
	case e.Delisted:
		return formPicture
	default:
		return formEmotion
	}
}

// reactName is what a person calls the emoji. The key stands in for one this
// build has no entry for, which is also the only spelling such an emoji has.
func reactName(key string) string {
	if e, ok := emoji.ByKey(key); ok {
		return e.Name()
	}
	return key
}

// reactResult is what the command answers with, and the shape a script reads.
type reactResult struct {
	MessageID string    `json:"message_id"`
	Emoji     string    `json:"emoji"`
	Form      reactForm `json:"form"`
	// ReplyID and ChatID name the message a reply left behind, and are empty
	// for a reaction, which is not a message.
	ReplyID string `json:"reply_message_id,omitempty"`
	ChatID  string `json:"chat_id,omitempty"`
}

// stayInThread reports whether a reply to x belongs inside x's thread. A
// thread reply carries no position of its own, which is what tells it from a
// message in the chat's main flow; answering one in the main flow would drag
// the exchange out of the thread it was held in.
func stayInThread(x store.Message) bool { return x.ThreadID != "" && x.MessagePosition < 0 }

func (a *App) reactCmd() *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:   "react <message_id> --emoji <EMOJI_TYPE>",
		Short: "React to a message, replying with the emoji where Feishu refuses it as a reaction",
		Long: "Feishu refuses two kinds of emoji as a reaction. Another tenant's culture emoji goes\n" +
			"inside a message fine, so it is replied with; one the client has withdrawn reaches the\n" +
			"other side as \"[Sensitive emoji]\" however it is named, so its picture is replied with\n" +
			"instead. Which of the three happened is the `form` of the answer.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("pass --emoji")
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			res, err := a.react(ctx, st, args[0], key)
			if err != nil {
				return err
			}
			return a.printReacted(res)
		},
	}
	cmd.Flags().StringVar(&key, "emoji", "", "emoji_type, as the client spells it (case-sensitive): DONE, THUMBSUP, Get")
	// 200 opaque, case-sensitive keys is what completion is for: nobody
	// remembers that 赞 is THUMBSUP and 破涕为笑 is Get.
	_ = cmd.RegisterFlagCompletionFunc("emoji", func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		var out []string
		for _, e := range emoji.All() {
			if e.Offerable() && strings.HasPrefix(strings.ToLower(e.Key), strings.ToLower(prefix)) {
				out = append(out, e.Key+"\t"+e.Name())
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// react puts key on messageID in whichever form Feishu accepts it.
func (a *App) react(ctx context.Context, st *store.Store, messageID, key string) (reactResult, error) {
	res := reactResult{MessageID: messageID, Emoji: key, Form: formFor(key)}
	if res.Form == formReaction {
		// Through the syncer, so the stored summary carries the reaction the
		// moment it lands rather than at the next refresh pass.
		return res, a.syncer(st).React(ctx, messageID, key, true)
	}
	body, err := a.reactBody(ctx, res.Form, key)
	if err != nil {
		return res, err
	}
	// A message the store has never seen is still reactable — Feishu answers
	// by id alone — so a miss decides nothing but which flow the reply lands
	// in, and the main flow is where a message without a thread belongs.
	parent, miss := st.GetMessage(ctx, messageID)
	sent, err := a.client().Reply(ctx, messageID, body, miss == nil && stayInThread(parent), uuid.NewString())
	if err != nil {
		return res, err
	}
	a.ingestSent(ctx, st, sent)
	res.ReplyID, res.ChatID = sent.MessageID, sent.ChatID
	return res, nil
}

// reactBody is the message a refused emoji is replied with.
func (a *App) reactBody(ctx context.Context, form reactForm, key string) (larkcli.Outgoing, error) {
	if form == formEmotion {
		return larkcli.Emotion(key), nil
	}
	// The pictures are cut from the sheet this binary carries, so a build with
	// a newer sheet cuts them again rather than asking anyone to.
	if _, err := emoji.Ensure(a.cfg.DataDir); err != nil {
		return larkcli.Outgoing{}, err
	}
	imageKey, err := a.client().UploadImage(ctx, emoji.Path(a.cfg.DataDir, key))
	if err != nil {
		return larkcli.Outgoing{}, err
	}
	return larkcli.Image(imageKey), nil
}

func (a *App) printReacted(res reactResult) error {
	if a.json() {
		return a.printJSON(res)
	}
	name := reactName(res.Emoji)
	switch res.Form {
	case formEmotion:
		fmt.Fprintf(a.Out, "%s takes no %s reaction; replied with it instead (%s)\n", res.MessageID, name, res.ReplyID)
	case formPicture:
		fmt.Fprintf(a.Out, "%s takes no %s reaction; replied with its picture instead (%s)\n", res.MessageID, name, res.ReplyID)
	default:
		fmt.Fprintf(a.Out, "reacted to %s with %s\n", res.MessageID, name)
	}
	return nil
}
