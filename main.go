package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

var urlRegex = regexp.MustCompile(`https?://[^\s]+`)

// Locales stores all translations
var Locales map[string]map[string]string

// UserData persists user-specific settings
type UserData struct {
	APIKey   string `json:"api_key"`
	Language string `json:"language"` // "en", "ru", etc.
}

// FivemanageResponse represents the JSON response from Fivemanage API
type FivemanageResponse struct {
	Status string `json:"status"`
	Data   struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"data"`
}

// UserStore persists Discord User ID → UserData mappings
type UserStore struct {
	mu    sync.RWMutex
	users map[string]UserData
	path  string
}

var (
	store      *UserStore
	httpClient = &http.Client{Timeout: 120 * time.Second}
	appID      string
	encryptionKey []byte
)

func main() {
	_ = godotenv.Load()

	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_TOKEN is not set")
	}
	appID = os.Getenv("APPLICATION_ID")
	if appID == "" {
		log.Fatal("APPLICATION_ID is not set")
	}

	encKeyHex := os.Getenv("ENCRYPTION_KEY")
	if encKeyHex != "" {
		keyBytes, err := hex.DecodeString(encKeyHex)
		if err == nil && len(keyBytes) == 32 {
			encryptionKey = keyBytes
			log.Println("AES Encryption enabled for API keys.")
		} else {
			log.Println("WARNING: ENCRYPTION_KEY is invalid. It must be 32 bytes hex encoded. Proceeding without encryption.")
		}
	} else {
		log.Println("WARNING: ENCRYPTION_KEY is not set. API keys will be stored in plain text.")
	}

	keysFile := os.Getenv("KEYS_FILE")
	if keysFile == "" {
		keysFile = "keys.json"
	}
	store = NewUserStore(keysFile)
	if err := store.Load(); err != nil {
		log.Printf("User store file not found (will be created): %v", err)
	}

	loadLocales()

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatalf("Failed to create Discord session: %v", err)
	}

	dg.AddHandler(onReady)
	dg.AddHandler(onMessageCreate)
	dg.AddHandler(onInteractionCreate)
	dg.AddHandler(onGuildMemberAdd)

	dg.Identify.Intents = discordgo.IntentsDirectMessages |
		discordgo.IntentsGuildMembers |
		discordgo.IntentsGuilds

	if err := dg.Open(); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer dg.Close()

	registerCommands(dg)

	log.Println("Bot started.")
	startApp()
	log.Println("Shutting down...")
}

// ─── User Store ──────────────────────────────────────────────────────────────

func NewUserStore(path string) *UserStore {
	return &UserStore{
		users: make(map[string]UserData),
		path:  path,
	}
}

func (s *UserStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.users); err != nil {
		return err
	}

	if len(encryptionKey) > 0 {
		changed := false
		for id, user := range s.users {
			if user.APIKey != "" {
				_, wasEncrypted := decryptData(user.APIKey)
				if !wasEncrypted {
					user.APIKey = encryptData(user.APIKey)
					s.users[id] = user
					changed = true
				}
			}
		}
		if changed {
			saveData, _ := json.MarshalIndent(s.users, "", "  ")
			os.WriteFile(s.path, saveData, 0644)
			log.Println("Migrated existing API keys to encrypted format.")
		}
	}
	return nil
}

func (s *UserStore) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.MarshalIndent(s.users, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

func (s *UserStore) SetKey(userID, apiKey string) error {
	s.mu.Lock()
	user := s.users[userID]
	user.APIKey = encryptData(apiKey)
	s.users[userID] = user
	s.mu.Unlock()
	return s.Save()
}

func (s *UserStore) SetLanguage(userID, lang string) error {
	s.mu.Lock()
	user := s.users[userID]
	user.Language = lang
	s.users[userID] = user
	s.mu.Unlock()
	return s.Save()
}

func (s *UserStore) Get(userID string) (UserData, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[userID]
	if ok {
		decrypted, _ := decryptData(user.APIKey)
		user.APIKey = decrypted
	}
	return user, ok
}

func (s *UserStore) DeleteKey(userID string) error {
	s.mu.Lock()
	user := s.users[userID]
	user.APIKey = ""
	s.users[userID] = user
	s.mu.Unlock()
	return s.Save()
}

// ─── Encryption ──────────────────────────────────────────────────────────────

func encryptData(data string) string {
	if len(encryptionKey) == 0 || data == "" {
		return data
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return data
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return data
	}
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return data
	}
	ciphertext := aesGCM.Seal(nonce, nonce, []byte(data), nil)
	return hex.EncodeToString(ciphertext)
}

func decryptData(data string) (string, bool) {
	if len(encryptionKey) == 0 || data == "" {
		return data, false
	}
	ciphertext, err := hex.DecodeString(data)
	if err != nil {
		return data, false
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return data, false
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return data, false
	}
	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return data, false
	}
	nonce, cipherData := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, cipherData, nil)
	if err != nil {
		return data, false
	}
	return string(plaintext), true
}

// ─── Locales ─────────────────────────────────────────────────────────────────

func loadLocales() {
	Locales = make(map[string]map[string]string)
	
	files, err := os.ReadDir("lang")
	if err != nil {
		log.Fatalf("Error reading lang directory: %v", err)
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".json" {
			lang := strings.TrimSuffix(file.Name(), ".json")
			data, err := os.ReadFile(filepath.Join("lang", file.Name()))
			if err != nil {
				log.Printf("Error reading %s: %v", file.Name(), err)
				continue
			}

			var translations map[string]string
			if err := json.Unmarshal(data, &translations); err != nil {
				log.Printf("Error parsing %s: %v", file.Name(), err)
				continue
			}

			Locales[lang] = translations
			log.Printf("Loaded language: %s", lang)
		}
	}

	if len(Locales) == 0 {
		log.Fatal("No language files found in 'lang' directory")
	}
}

func T(userID, key string) string {
	lang := "en" // default
	if user, ok := store.Get(userID); ok && user.Language != "" {
		lang = user.Language
	}

	if l, ok := Locales[lang]; ok {
		if val, ok := l[key]; ok {
			return val
		}
	}

	// Fallback to English
	if val, ok := Locales["en"][key]; ok {
		return val
	}

	return key
}

func getLang(userID string) string {
	if user, ok := store.Get(userID); ok && user.Language != "" {
		return user.Language
	}
	return "en"
}

// ─── Slash Commands ──────────────────────────────────────────────────────────

func registerCommands(s *discordgo.Session) {
	guildID := os.Getenv("DISCORD_GUILD_ID")
	if guildID != "" {
		log.Printf("Registering commands for guild: %s", guildID)
	} else {
		log.Println("Registering global commands (may take up to 1 hour)")
	}

	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "setkey",
			Description: "Set your Fivemanage API key for file uploads",
			DescriptionLocalizations: &map[discordgo.Locale]string{
				discordgo.Russian: "Установить ваш Fivemanage API ключ для загрузки файлов",
			},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "apikey",
					Description: "Your API key from https://app.fivemanage.com → API Keys",
					NameLocalizations: map[discordgo.Locale]string{
						discordgo.Russian: "apikey",
					},
					DescriptionLocalizations: map[discordgo.Locale]string{
						discordgo.Russian: "Ваш API ключ с https://app.fivemanage.com → API Keys",
					},
					Required: true,
				},
			},
		},
		{
			Name:        "removekey",
			Description: "Remove your saved API key",
			DescriptionLocalizations: &map[discordgo.Locale]string{
				discordgo.Russian: "Удалить ваш сохранённый API ключ",
			},
		},
		{
			Name:        "status",
			Description: "Check if your API key is configured",
			DescriptionLocalizations: &map[discordgo.Locale]string{
				discordgo.Russian: "Проверить, настроен ли ваш API ключ",
			},
		},
		{
			Name:        "help",
			Description: "Show bot usage instructions",
			DescriptionLocalizations: &map[discordgo.Locale]string{
				discordgo.Russian: "Показать инструкцию по использованию бота",
			},
		},
		{
			Name:        "start",
			Description: "Show the bot welcome message",
			DescriptionLocalizations: &map[discordgo.Locale]string{
				discordgo.Russian: "Показать приветственное сообщение бота",
			},
		},
		{
			Name:        "language",
			Description: "Change the bot's language",
			DescriptionLocalizations: &map[discordgo.Locale]string{
				discordgo.Russian: "Изменить язык бота",
			},
		},
	}

	for _, cmd := range commands {
		_, err := s.ApplicationCommandCreate(appID, guildID, cmd)
		if err != nil {
			log.Printf("Failed to register command '%s': %v", cmd.Name, err)
		} else {
			log.Printf("Command registered: /%s", cmd.Name)
		}
	}
}

func onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Handle button clicks
	if i.Type == discordgo.InteractionMessageComponent {
		handleButton(s, i)
		return
	}

	// Handle modal submissions
	if i.Type == discordgo.InteractionModalSubmit {
		handleModal(s, i)
		return
	}

	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()
	userID := ""
	if i.User != nil {
		userID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}

	// Check for language first
	if _, ok := store.Get(userID); !ok {
		sendLanguageSelection(s, i.Interaction)
		return
	}

	switch data.Name {
	case "language":
		sendLanguageSelection(s, i.Interaction)

	case "setkey":
		apiKey := data.Options[0].StringValue()

		// Defer response to prevent timeout
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags: discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			log.Printf("Failed to defer /setkey: %v", err)
			return
		}

		valid, err := validateFivemanageKey(apiKey)
		if err != nil || !valid {
			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: T(userID, "api_key_invalid") + "\n\nError: " + fmt.Sprint(err),
				Flags:   discordgo.MessageFlagsEphemeral,
			})
			return
		}

		if err := store.SetKey(userID, apiKey); err != nil {
			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: "❌ Error saving key: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			})
			return
		}

		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content:    T(userID, "api_key_saved"),
			Flags:      discordgo.MessageFlagsEphemeral,
			Components: welcomeComponents(userID),
		})
		log.Printf("User %s saved API key", userID)

	case "removekey":
		user, _ := store.Get(userID)
		if user.APIKey == "" {
			respond(s, i, T(userID, "no_api_key"), true)
			return
		}
		if err := store.DeleteKey(userID); err != nil {
			respond(s, i, "❌ Error deleting key: "+err.Error(), true)
			return
		}
		respond(s, i, T(userID, "api_key_removed"), true)
		log.Printf("User %s deleted API key", userID)

	case "status":
		user, _ := store.Get(userID)
		if user.APIKey != "" {
			respond(s, i, T(userID, "api_key_configured")+"\n\n"+T(userID, "usage_info_note"), true)
		} else {
			respondWithComponents(s, i, T(userID, "api_key_not_set"), welcomeComponents(userID), true)
		}

	case "help":
		respondWithComponents(s, i, T(userID, "help_text"), welcomeComponents(userID), true)

	case "start", "st":
		respondWithComponents(s, i, T(userID, "welcome_text"), welcomeComponents(userID), false)
	}
}


func respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string, ephemeral bool) {
	userID := ""
	if i.User != nil {
		userID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}

	if i.Interaction.GuildID != "" {
		dmChannel, err := s.UserChannelCreate(userID)
		if err == nil {
			_, dmErr := s.ChannelMessageSend(dmChannel.ID, content)
			if dmErr == nil {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "📬 " + T(userID, "dm_sent"),
						Flags:   discordgo.MessageFlagsEphemeral,
					},
				})
				return
			}
		}
	}

	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   flags,
		},
	})
}

func respondWithComponents(s *discordgo.Session, i *discordgo.InteractionCreate, content string, components []discordgo.MessageComponent, ephemeral bool) {
	userID := ""
	if i.User != nil {
		userID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}

	if i.Interaction.GuildID != "" {
		dmChannel, err := s.UserChannelCreate(userID)
		if err == nil {
			_, dmErr := s.ChannelMessageSendComplex(dmChannel.ID, &discordgo.MessageSend{
				Content:    content,
				Components: components,
			})
			if dmErr == nil {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "📬 " + T(userID, "dm_sent"),
						Flags:   discordgo.MessageFlagsEphemeral,
					},
				})
				return
			}
		}
	}

	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    content,
			Components: components,
			Flags:      flags,
		},
	})
}

// ─── Message Handler ─────────────────────────────────────────────────────────

func onGuildMemberAdd(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	if m.User.Bot {
		return
	}

	channel, err := s.UserChannelCreate(m.User.ID)
	if err != nil {
		log.Printf("Failed to create DM channel for %s: %v", m.User.Username, err)
		return
	}

	// Always offer language selection to new members
	_, err = s.ChannelMessageSendComplex(channel.ID, &discordgo.MessageSend{
		Content:    "Please select your language / Пожалуйста, выберите язык:",
		Components: languageComponents(),
	})
	if err != nil {
		log.Printf("Failed to send language selection to %s: %v", m.User.Username, err)
	}
}

func welcomeComponents(userID string) []discordgo.MessageComponent {
	user, _ := store.Get(userID)
	hasKey := user.APIKey != ""

	var buttons []discordgo.MessageComponent

	if !hasKey {
		buttons = append(buttons, discordgo.Button{
			Label:    T(userID, "enter_api_key"),
			Style:    discordgo.SuccessButton,
			CustomID: "open_api_modal",
		})
	} else {
		// If key is already set, show link to Dashboard instead of entry button
		buttons = append(buttons, discordgo.Button{
			Label: T(userID, "dashboard_btn"),
			Style: discordgo.LinkButton,
			URL:   "https://app.fivemanage.com",
		})
		buttons = append(buttons, discordgo.Button{
			Label:    T(userID, "remove_api_key_btn"),
			Style:    discordgo.DangerButton,
			CustomID: "remove_api_key_confirm",
		})
	}

	buttons = append(buttons, discordgo.Button{
		Label:    T(userID, "instruction"),
		Style:    discordgo.PrimaryButton,
		CustomID: "show_help",
	})

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: buttons,
		},
	}
}

func languageComponents() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "English 🇺🇸",
					Style:    discordgo.PrimaryButton,
					CustomID: "set_lang_en",
				},
				discordgo.Button{
					Label:    "Русский 🇷🇺",
					Style:    discordgo.PrimaryButton,
					CustomID: "set_lang_ru",
				},
			},
		},
	}
}

func sendLanguageSelection(s *discordgo.Session, i *discordgo.Interaction) {
	s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    "Please select your language / Пожалуйста, выберите язык:",
			Components: languageComponents(),
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

func onReady(s *discordgo.Session, r *discordgo.Ready) {
	log.Printf("Bot started as %s#%s", r.User.Username, r.User.Discriminator)

	s.UpdateStatusComplex(discordgo.UpdateStatusData{
		Activities: []*discordgo.Activity{
			{
				Name: "📸 /help — upload to Fivemanage / загрузка на Fivemanage",
				Type: discordgo.ActivityTypeCustom,
			},
		},
		Status: "online",
	})
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Only process DMs
	channel, err := s.Channel(m.ChannelID)
	if err != nil {
		return
	}
	if channel.Type != discordgo.ChannelTypeDM {
		return
	}

	if len(m.Attachments) == 0 {
		s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
			Content:    T(m.Author.ID, "greeting"),
			Components: welcomeComponents(m.Author.ID),
		})
		return
	}

	user, ok := store.Get(m.Author.ID)
	if !ok || user.APIKey == "" {
		s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
			Content:    T(m.Author.ID, "api_key_not_set"),
			Components: welcomeComponents(m.Author.ID),
		})
		return
	}

	apiKey := user.APIKey

	// Process attachments
	for _, attachment := range m.Attachments {
		go processAttachment(s, m.ChannelID, attachment, apiKey, m.Author.ID)
	}

	// Process URLs in content
	urls := urlRegex.FindAllString(m.Content, -1)
	for _, url := range urls {
		// Avoid processing Discord's own attachment URLs if they are already in m.Attachments
		isAttachment := false
		for _, att := range m.Attachments {
			if att.URL == url || att.ProxyURL == url {
				isAttachment = true
				break
			}
		}
		if !isAttachment {
			go processURL(s, m.ChannelID, url, apiKey, m.Author.ID)
		}
	}
}

// ─── Upload Logic ────────────────────────────────────────────────────────────

func processAttachment(s *discordgo.Session, channelID string, attachment *discordgo.MessageAttachment, apiKey string, userID string) {
	log.Printf("Uploading: %s (%d bytes)", attachment.Filename, attachment.Size)

	statusMsg, err := s.ChannelMessageSend(channelID, fmt.Sprintf(T(userID, "uploading"), attachment.Filename))
	if err != nil {
		log.Printf("Error sending status: %v", err)
		return
	}

	fileData, err := downloadFile(attachment.URL)
	if err != nil {
		editMessage(s, channelID, statusMsg.ID, fmt.Sprintf(T(userID, "download_failed"), attachment.Filename, err))
		return
	}

	result, err := uploadToFivemanage(fileData, attachment.Filename, apiKey)
	if err != nil {
		editMessage(s, channelID, statusMsg.ID, fmt.Sprintf(T(userID, "upload_failed"), attachment.Filename, err))
		return
	}

	content := fmt.Sprintf(T(userID, "upload_success"), attachment.Filename, result.Data.URL, result.Data.URL)
	deleteButtonID := fmt.Sprintf("delete:%s:%s", result.Data.ID, userID)

	_, err = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel: channelID,
		ID:      statusMsg.ID,
		Content: &content,
		Components: &[]discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    T(userID, "delete"),
						Style:    discordgo.DangerButton,
						CustomID: deleteButtonID,
					},
				},
			},
		},
	})
	if err != nil {
		log.Printf("Failed to edit message: %v", err)
	}

	log.Printf("Uploaded: %s → %s (id: %s)", attachment.Filename, result.Data.URL, result.Data.ID)
}

func processURL(s *discordgo.Session, channelID string, url string, apiKey string, userID string) {
	filename := filepath.Base(url)
	if strings.Contains(filename, "?") {
		filename = strings.Split(filename, "?")[0]
	}
	if filename == "" || filename == "." || filename == "/" {
		filename = "downloaded_file"
	}

	log.Printf("Downloading from URL: %s", url)

	statusMsg, err := s.ChannelMessageSend(channelID, fmt.Sprintf(T(userID, "uploading"), filename))
	if err != nil {
		return
	}

	fileData, err := downloadFile(url)
	if err != nil {
		editMessage(s, channelID, statusMsg.ID, fmt.Sprintf(T(userID, "download_failed"), filename, err))
		return
	}

	result, err := uploadToFivemanage(fileData, filename, apiKey)
	if err != nil {
		editMessage(s, channelID, statusMsg.ID, fmt.Sprintf(T(userID, "upload_failed"), filename, err))
		return
	}

	// Success response
	s.ChannelMessageDelete(channelID, statusMsg.ID)
	s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: fmt.Sprintf(T(userID, "upload_success"), filename, result.Data.URL, result.Data.URL),
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    T(userID, "delete"),
						Style:    discordgo.DangerButton,
						CustomID: fmt.Sprintf("delete:%s:%s", result.Data.ID, userID),
					},
				},
			},
		},
	})
}

func downloadFile(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET ошибка: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("неожиданный статус: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func uploadToFivemanage(fileData []byte, filename, apiKey string) (*FivemanageResponse, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("создание формы: %w", err)
	}

	if _, err := io.Copy(part, bytes.NewReader(fileData)); err != nil {
		return nil, fmt.Errorf("копирование данных: %w", err)
	}

	metadata, _ := json.Marshal(map[string]string{"name": filename})
	_ = writer.WriteField("metadata", string(metadata))

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("закрытие формы: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.fivemanage.com/api/v3/file", &buf)
	if err != nil {
		return nil, fmt.Errorf("создание запроса: %w", err)
	}

	req.Header.Set("Authorization", apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP запрос ошибка: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("чтение ответа: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("ошибка API (статус %d): %s", resp.StatusCode, truncate(string(body), 200))
	}

	var result FivemanageResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("разбор ответа: %w (тело: %s)", err, truncate(string(body), 200))
	}

	if result.Status != "ok" {
		return nil, fmt.Errorf("API вернул статус: %s", result.Status)
	}

	return &result, nil
}

func validateFivemanageKey(apiKey string) (bool, error) {
	req, err := http.NewRequest("GET", "https://api.fivemanage.com/api/v3/file/presigned-url", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, fmt.Errorf("неверный ключ — проверьте API токен")
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("неожиданный статус: %d, тело: %s", resp.StatusCode, string(body))
	}

	return true, nil
}

// ─── Button Handler ──────────────────────────────────────────────────────────

func handleButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID
	userID := ""
	if i.User != nil {
		userID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}

	if strings.HasPrefix(customID, "set_lang_") {
		lang := strings.TrimPrefix(customID, "set_lang_")
		store.SetLanguage(userID, lang)

		// Send welcome message in chosen language
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    T(userID, "welcome_text"),
				Components: welcomeComponents(userID),
			},
		})
		return
	}

	if customID == "open_api_modal" {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseModal,
			Data: &discordgo.InteractionResponseData{
				CustomID: "api_key_modal",
				Title:    T(userID, "modal_title"),
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{
						Components: []discordgo.MessageComponent{
							discordgo.TextInput{
								CustomID:    "api_key_input",
								Label:       T(userID, "modal_label"),
								Style:       discordgo.TextInputShort,
								Placeholder: T(userID, "modal_placeholder"),
								Required:    true,
							},
						},
					},
				},
			},
		})
		return
	}

	if customID == "show_help" {
		respond(s, i, T(userID, "help_text"), true)
		return
	}

	if customID == "remove_api_key_confirm" {
		if err := store.DeleteKey(userID); err != nil {
			respond(s, i, "❌ Error deleting key: "+err.Error(), true)
			return
		}
		respond(s, i, T(userID, "api_key_removed"), true)
		log.Printf("User %s deleted API key via button", userID)
		return
	}

	if !strings.HasPrefix(customID, "delete:") {
		return
	}

	parts := strings.SplitN(customID, ":", 3)
	if len(parts) != 3 {
		return
	}

	fileID := parts[1]
	ownerID := parts[2]

	// Get the user who clicked
	clickerID := ""
	if i.User != nil {
		clickerID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		clickerID = i.Member.User.ID
	}

	// Only the uploader can delete
	if clickerID != ownerID {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: T(clickerID, "only_owner_delete"),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	user, ok := store.Get(ownerID)
	if !ok || user.APIKey == "" {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: T(clickerID, "api_key_not_found_delete"),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	apiKey := user.APIKey

	// Delete from Fivemanage
	err := deleteFromFivemanage(fileID, apiKey)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf(T(clickerID, "delete_error"), err),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Update the original message — remove button, mark as deleted
	deletedContent := T(clickerID, "deleted")
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    deletedContent,
			Components: []discordgo.MessageComponent{},
		},
	})

	log.Printf("Deleted file %s by user %s", fileID, ownerID)
}

func handleModal(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()
	log.Printf("Modal submit received, ID: %s", data.CustomID)

	if data.CustomID != "api_key_modal" {
		log.Printf("Unknown modal ID: %s", data.CustomID)
		return
	}

	// Safety check for components
	if len(data.Components) == 0 {
		log.Println("Error: modal has no components")
		return
	}

	row, ok := data.Components[0].(*discordgo.ActionsRow)
	if !ok || len(row.Components) == 0 {
		log.Println("Error: failed to get ActionsRow or its components")
		return
	}

	input, ok := row.Components[0].(*discordgo.TextInput)
	if !ok {
		log.Println("Error: first component is not a TextInput")
		return
	}

	apiKey := input.Value
	log.Printf("Extracted API key (length: %d)", len(apiKey))
	
	var userID string
	if i.User != nil {
		userID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}

	if userID == "" {
		log.Println("Error: failed to determine userID")
		return
	}

	log.Printf("Deferring response for user %s", userID)
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		log.Printf("Failed to send DeferredResponse: %v", err)
		return
	}

	log.Println("Validating key with Fivemanage...")
	valid, err := validateFivemanageKey(apiKey)
	if err != nil || !valid {
		log.Printf("Validation failed: %v", err)
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: T(userID, "api_key_invalid"),
			Flags:   discordgo.MessageFlagsEphemeral,
		})
		return
	}

	log.Println("Saving key...")
	if err := store.SetKey(userID, apiKey); err != nil {
		log.Printf("Save error: %v", err)
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: "❌ Save error: " + err.Error(),
			Flags:   discordgo.MessageFlagsEphemeral,
		})
		return
	}

	log.Println("Key saved successfully")
	s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content:    T(userID, "api_key_saved"),
		Flags:      discordgo.MessageFlagsEphemeral,
		Components: welcomeComponents(userID),
	})
}

func deleteFromFivemanage(fileID, apiKey string) error {
	req, err := http.NewRequest("DELETE", "https://api.fivemanage.com/api/v3/file/"+fileID, nil)
	if err != nil {
		return fmt.Errorf("создание запроса: %w", err)
	}
	req.Header.Set("Authorization", apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP запрос: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API ошибка (статус %d): %s", resp.StatusCode, truncate(string(body), 200))
	}

	return nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func editMessage(s *discordgo.Session, channelID, messageID, content string) {
	if _, err := s.ChannelMessageEdit(channelID, messageID, content); err != nil {
		log.Printf("Failed to edit message: %v", err)
	}
}

func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
