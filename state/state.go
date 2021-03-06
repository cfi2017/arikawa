// Package state provides interfaces for a local or remote state, as well as
// abstractions around the REST API and Gateway events.
package state

import (
	"sync"

	"github.com/diamondburned/arikawa/discord"
	"github.com/diamondburned/arikawa/gateway"
	"github.com/diamondburned/arikawa/handler"
	"github.com/diamondburned/arikawa/session"
	"github.com/pkg/errors"
)

var (
	MaxFetchMembers uint = 1000
	MaxFetchGuilds  uint = 100
)

type State struct {
	*session.Session
	Store

	// *: State doesn't actually keep track of pinned messages.

	// Ready is not updated by the state.
	Ready gateway.ReadyEvent

	// StateLog logs all errors that come from the state cache. This includes
	// not found errors. Defaults to a no-op, as state errors aren't that
	// important.
	StateLog func(error)

	// PreHandler is the manual hook that is executed before the State handler
	// is. This should only be used for low-level operations.
	// It's recommended to set Synchronous to true if you mutate the events.
	PreHandler *handler.Handler // default nil

	unhooker func()

	// List of channels with few messages, so it doesn't bother hitting the API
	// again.
	fewMessages []discord.Snowflake
	fewMutex    sync.Mutex
}

func NewFromSession(s *session.Session, store Store) (*State, error) {
	state := &State{
		Session:  s,
		Store:    store,
		StateLog: func(err error) {},
	}

	s.ErrorLog = state.ErrorLog

	return state, state.hookSession()
}

func New(token string) (*State, error) {
	return NewWithStore(token, NewDefaultStore(nil))
}

func NewWithStore(token string, store Store) (*State, error) {
	s, err := session.New(token)
	if err != nil {
		return nil, err
	}

	return NewFromSession(s, store)
}

// Unhook removes all state handlers from the session handlers.
func (s *State) Unhook() {
	s.unhooker()
}

//// Helper methods

func (s *State) AuthorDisplayName(message discord.Message) string {
	if !message.GuildID.Valid() {
		return message.Author.Username
	}

	n, err := s.MemberDisplayName(message.GuildID, message.Author.ID)
	if err != nil {
		return message.Author.Username
	}

	return n
}

func (s *State) MemberDisplayName(
	guildID, userID discord.Snowflake) (string, error) {

	member, err := s.Member(guildID, userID)
	if err != nil {
		return "", err
	}

	if member.Nick == "" {
		return member.User.Username, nil
	}

	return member.Nick, nil
}

func (s *State) AuthorColor(message discord.Message) discord.Color {
	if !message.GuildID.Valid() {
		return discord.DefaultMemberColor
	}

	return s.MemberColor(message.GuildID, message.Author.ID)
}

func (s *State) MemberColor(guildID, userID discord.Snowflake) discord.Color {
	member, err := s.Member(guildID, userID)
	if err != nil {
		return discord.DefaultMemberColor
	}

	guild, err := s.Guild(guildID)
	if err != nil {
		return discord.DefaultMemberColor
	}

	return discord.MemberColor(*guild, *member)
}

////

func (s *State) Permissions(
	channelID, userID discord.Snowflake) (discord.Permissions, error) {

	ch, err := s.Channel(channelID)
	if err != nil {
		return 0, errors.Wrap(err, "Failed to get channel")
	}

	g, err := s.Guild(ch.GuildID)
	if err != nil {
		return 0, errors.Wrap(err, "Failed to get guild")
	}

	m, err := s.Member(ch.GuildID, userID)
	if err != nil {
		return 0, errors.Wrap(err, "Failed to get member")
	}

	return discord.CalcOverwrites(*g, *ch, *m), nil
}

////

func (s *State) Self() (*discord.User, error) {
	u, err := s.Store.Self()
	if err == nil {
		return u, nil
	}

	u, err = s.Session.Me()
	if err != nil {
		return nil, err
	}

	return u, s.Store.SelfSet(u)
}

////

func (s *State) Channel(id discord.Snowflake) (*discord.Channel, error) {
	c, err := s.Store.Channel(id)
	if err == nil {
		return c, nil
	}

	c, err = s.Session.Channel(id)
	if err != nil {
		return nil, err
	}

	return c, s.Store.ChannelSet(c)
}

func (s *State) Channels(guildID discord.Snowflake) ([]discord.Channel, error) {
	c, err := s.Store.Channels(guildID)
	if err == nil {
		return c, nil
	}

	c, err = s.Session.Channels(guildID)
	if err != nil {
		return nil, err
	}

	for _, ch := range c {
		if err := s.Store.ChannelSet(&ch); err != nil {
			return nil, err
		}
	}

	return c, nil
}

////

func (s *State) Emoji(
	guildID, emojiID discord.Snowflake) (*discord.Emoji, error) {

	e, err := s.Store.Emoji(guildID, emojiID)
	if err == nil {
		return e, nil
	}

	es, err := s.Session.Emojis(guildID)
	if err != nil {
		return nil, err
	}

	if err := s.Store.EmojiSet(guildID, es); err != nil {
		return nil, err
	}

	for _, e := range es {
		if e.ID == emojiID {
			return &e, nil
		}
	}

	return nil, ErrStoreNotFound
}

func (s *State) Emojis(guildID discord.Snowflake) ([]discord.Emoji, error) {
	e, err := s.Store.Emojis(guildID)
	if err == nil {
		return e, nil
	}

	es, err := s.Session.Emojis(guildID)
	if err != nil {
		return nil, err
	}

	return es, s.Store.EmojiSet(guildID, es)
}

////

func (s *State) Guild(id discord.Snowflake) (*discord.Guild, error) {
	c, err := s.Store.Guild(id)
	if err == nil {
		return c, nil
	}

	c, err = s.Session.Guild(id)
	if err != nil {
		return nil, err
	}

	return c, s.Store.GuildSet(c)
}

// Guilds will only fill a maximum of 100 guilds from the API.
func (s *State) Guilds() ([]discord.Guild, error) {
	c, err := s.Store.Guilds()
	if err == nil {
		return c, nil
	}

	c, err = s.Session.Guilds(MaxFetchGuilds)
	if err != nil {
		return nil, err
	}

	for _, ch := range c {
		if err := s.Store.GuildSet(&ch); err != nil {
			return nil, err
		}
	}

	return c, nil
}

////

func (s *State) Member(
	guildID, userID discord.Snowflake) (*discord.Member, error) {

	m, err := s.Store.Member(guildID, userID)
	if err == nil {
		return m, nil
	}

	m, err = s.Session.Member(guildID, userID)
	if err != nil {
		return nil, err
	}

	return m, s.Store.MemberSet(guildID, m)
}

func (s *State) Members(guildID discord.Snowflake) ([]discord.Member, error) {
	ms, err := s.Store.Members(guildID)
	if err == nil {
		return ms, nil
	}

	ms, err = s.Session.Members(guildID, MaxFetchMembers)
	if err != nil {
		return nil, err
	}

	for _, m := range ms {
		if err := s.Store.MemberSet(guildID, &m); err != nil {
			return nil, err
		}
	}

	return ms, s.Gateway.RequestGuildMembers(gateway.RequestGuildMembersData{
		GuildID:   []discord.Snowflake{guildID},
		Presences: true,
	})
}

////

func (s *State) Message(
	channelID, messageID discord.Snowflake) (*discord.Message, error) {

	m, err := s.Store.Message(channelID, messageID)
	if err == nil {
		return m, nil
	}

	m, err = s.Session.Message(channelID, messageID)
	if err != nil {
		return nil, err
	}

	// Fill the GuildID, because Discord doesn't do it for us.
	c, err := s.Channel(channelID)
	if err == nil {
		// If it's 0, it's 0 anyway. We don't need a check here.
		m.GuildID = c.GuildID
	}

	return m, s.Store.MessageSet(m)
}

// Messages fetches maximum 100 messages from the API, if it has to. There is no
// limit if it's from the State storage.
func (s *State) Messages(channelID discord.Snowflake) ([]discord.Message, error) {
	// TODO: Think of a design that doesn't rely on MaxMessages().
	var maxMsgs = s.MaxMessages()

	ms, err := s.Store.Messages(channelID)
	if err == nil {
		// If the state already has as many messages as it can, skip the API.
		if maxMsgs <= len(ms) {
			return ms, nil
		}

		// Is the channel tiny?
		s.fewMutex.Lock()
		for _, ch := range s.fewMessages {
			if ch == channelID {
				// Yes, skip the state.
				s.fewMutex.Unlock()
				return ms, nil
			}
		}

		// No, fetch from the state.
		s.fewMutex.Unlock()
	}

	ms, err = s.Session.Messages(channelID, 100)
	if err != nil {
		return nil, err
	}

	// New messages fetched weirdly does not have GuildID filled. We'll try and
	// get it for consistency with incoming message creates.
	var guildID discord.Snowflake

	// A bit too convoluted, but whatever.
	c, err := s.Channel(channelID)
	if err == nil {
		// If it's 0, it's 0 anyway. We don't need a check here.
		guildID = c.GuildID
	}

	for i := range ms {
		// Set the guild ID, fine if it's 0 (it's already 0 anyway).
		ms[i].GuildID = guildID

		if err := s.Store.MessageSet(&ms[i]); err != nil {
			return nil, err
		}
	}

	if len(ms) < maxMsgs {
		// Tiny channel, store this.
		s.fewMutex.Lock()
		s.fewMessages = append(s.fewMessages, channelID)
		s.fewMutex.Unlock()

		return ms, nil
	}

	// Since the latest messages are at the end and we already know the maxMsgs,
	// we could slice this right away.
	return ms[:maxMsgs], nil
}

////

func (s *State) Presence(
	guildID, userID discord.Snowflake) (*discord.Presence, error) {

	return s.Store.Presence(guildID, userID)
}

func (s *State) Presences(
	guildID discord.Snowflake) ([]discord.Presence, error) {

	return s.Store.Presences(guildID)
}

////

func (s *State) Role(
	guildID, roleID discord.Snowflake) (*discord.Role, error) {

	r, err := s.Store.Role(guildID, roleID)
	if err == nil {
		return r, nil
	}

	rs, err := s.Session.Roles(guildID)
	if err != nil {
		return nil, err
	}

	var role *discord.Role

	for _, r := range rs {
		if r.ID == roleID {
			role = &r
		}

		if err := s.RoleSet(guildID, &r); err != nil {
			return role, err
		}
	}

	return role, nil
}

func (s *State) Roles(guildID discord.Snowflake) ([]discord.Role, error) {
	rs, err := s.Store.Roles(guildID)
	if err == nil {
		return rs, nil
	}

	rs, err = s.Session.Roles(guildID)
	if err != nil {
		return nil, err
	}

	for _, r := range rs {
		if err := s.RoleSet(guildID, &r); err != nil {
			return rs, err
		}
	}

	return rs, nil
}
