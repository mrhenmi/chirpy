package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"

	"github.com/djmarkymark007/chirpy/internal/database"
	"github.com/djmarkymark007/chirpy/internal/validate"
)

var db *database.Database
var config apiConfig

// TODO(Mark): custom 404 page
func status(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(http.StatusText(http.StatusOK)))
}

type returnError struct {
	Error string `json:"error"`
}

func respondWithError(w http.ResponseWriter, code int, msg string) {
	ret := returnError{Error: msg}
	data, err := json.Marshal(ret)
	if err != nil {
		log.Printf("Error marshalling JSON: %s\n", err)
		w.WriteHeader(500)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
}

func respondWithJson(w http.ResponseWriter, code int, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error marshalling JSON: %d\n", err)
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
}

func updateUser(w http.ResponseWriter, r *http.Request) {
	log.Print("updateUser: ")
	params := User{}
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&params)
	if err != nil {
		log.Print(err)
		respondWithError(w, 500, "Something went wrong")
		return
	}

	header := r.Header.Get("Authorization")
	token := strings.Split(header, " ")
	log.Print(token[len(token)-1])
	claims, err := jwt.ParseWithClaims(token[len(token)-1], &jwt.RegisteredClaims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			// Not sure if this should be fatal or not
			log.Fatalf("Token Method: %v want: %v", token.Method, jwt.SigningMethodHS256)
		}
		return []byte(config.jwtSecret), nil
	})

	if err != nil {
		log.Print(err)
		respondWithError(w, 401, "Unathorized")
		return
	}

	idString, err := claims.Claims.GetSubject()
	if err != nil {
		log.Print(err)
		respondWithError(w, 401, "Unathorized")
		return
	}

	id, err := strconv.Atoi(idString)
	if err != nil {
		log.Print(err)
		respondWithError(w, 401, "Unathorized")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Print(err)
		respondWithError(w, 500, "Something went wrong")
		return
	}

	db.UpdateUser(database.UserDatabase{Id: id, Email: params.Email, PasswordHash: passwordHash})

	respondWithJson(w, 200, database.User{Id: id, Email: params.Email})
}

func postLogin(w http.ResponseWriter, r *http.Request) {
	log.Print("postLogin: ")
	type parameters struct {
		Password         string `json:"password"`
		Email            string `json:"email"`
		ExpiresInSeconds int    `json:"expires_in_seconds"`
	}

	params := parameters{}
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&params)
	if err != nil {
		respondWithError(w, 500, "failed to decode JSON")
		return
	}

	user, err := db.GetUser(params.Email)
	if err != nil {
		log.Printf("postLogin: %s\n", err)
		respondWithError(w, 500, "failed to decode JSON")
		return
	}

	if bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(params.Password)) != nil {
		respondWithError(w, 401, "Unauthorized")
		return
	}

	log.Printf("recived expires in seconds: %v\n", params.ExpiresInSeconds)
	expires := 24 * 60 * 60
	if params.ExpiresInSeconds < expires && params.ExpiresInSeconds != 0 {
		expires = params.ExpiresInSeconds
	}
	log.Printf("expires in seconds: %v\n", expires)

	log.Printf("now: %v", time.Now().UTC())
	log.Printf("expires: %v", time.Now().UTC().Add(time.Duration(expires*int(time.Second))))
	claim := jwt.RegisteredClaims{Issuer: "chirpy",
		IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(time.Duration(expires * int(time.Second)))),
		Subject:   fmt.Sprint(user.Id)}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claim)
	jwtToken, err := token.SignedString([]byte(config.jwtSecret))
	if err != nil {
		log.Print(err)
		respondWithError(w, 500, "something went wrong")
		return
	}

	type UserWithjwt struct {
		Id    int    `json:"id"`
		Email string `json:"email"`
		Token string `json:"token"`
	}

	respondWithJson(w, 200, UserWithjwt{Id: user.Id, Email: user.Email, Token: jwtToken})
}

// TODO(Mark): Not sure if i like this
type User struct {
	Password string `json:"password"`
	Email    string `json:"email"`
}

func postUsers(w http.ResponseWriter, r *http.Request) {
	log.Print("postUsers: ")
	params := User{}
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&params)
	if err != nil {
		respondWithError(w, 500, "failed to decode JSON")
		return
	}

	alreadyExist, err := db.UserExist(params.Email)
	if err != nil {
		respondWithError(w, 500, "something went wrong")
		return
	}

	if alreadyExist {
		respondWithError(w, 401, "user email already used")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Print(err)
		respondWithError(w, 500, "Something went wrong")
		return
	}
	email, err := db.CreateUser(params.Email, passwordHash)
	if err != nil {
		log.Println(err)
		respondWithError(w, 500, "something went wrong")
	}

	respondWithJson(w, 201, email)
}

func postChirps(w http.ResponseWriter, r *http.Request) {
	log.Print("postChirps: ")
	type parameters struct {
		Body string `json:"body"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err != nil {
		log.Printf("Error decoding parameters: %s\n", err)
		respondWithError(w, 400, "Invalid JSON data")
		return
	}
	if len(params.Body) > 140 {
		respondWithError(w, 400, "Chirp is to long")
		return
	}

	chirp, err := db.CreateChirp(validate.ProfaneFilter(params.Body))
	if err != nil {
		log.Println(err)
		respondWithError(w, 500, "something went wrong")
	}

	respondWithJson(w, 201, chirp)
}

func getChirps(w http.ResponseWriter, r *http.Request) {
	log.Print("getChirps: ")
	chirps, err := db.GetChirps()
	if err != nil {
		log.Print(err)
		respondWithError(w, 500, "something went wrong")
	}

	respondWithJson(w, 200, chirps)
}

func getChirp(w http.ResponseWriter, r *http.Request) {
	log.Print("getChirp: ")
	path := r.PathValue("chirpID")
	fmt.Println(path)
	value, err := strconv.Atoi(path)
	if err != nil {
		respondWithError(w, 500, "could not convert to int")
		return
	}

	chirps, err := db.GetChirps()
	if err != nil {
		respondWithError(w, 500, "Failed to load chirps from database")
		return
	}
	if value > len(chirps) {
		respondWithError(w, 404, "chirp doesn't exist")
		return
	}

	var loc int = -1
	for index, chirp := range chirps {
		if chirp.Id == value {
			loc = index
		}
	}

	respondWithJson(w, 200, chirps[loc])
}

type apiConfig struct {
	fileserverHits int
	jwtSecret      string
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits++
		next.ServeHTTP(w, r)
	})
}

func middlewareLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("%s %s\n", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

const metricsMsg string = `<html>

<body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %d times!</p>
</body>

</html>
`

func (cfg *apiConfig) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Add("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf(metricsMsg, cfg.fileserverHits)
	w.Write([]byte(msg))
}

func (cfg *apiConfig) reset(w http.ResponseWriter, _ *http.Request) {
	cfg.fileserverHits = 0
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Hits reset to 0"))
}

func main() {
	const port = "8080"
	const filepathRoot = "."
	const path = "database.json"
	var err error

	err = godotenv.Load()
	if err != nil {
		log.Fatalf("Error loading .env file: %s", err)
	}

	config = apiConfig{fileserverHits: 0, jwtSecret: os.Getenv("JWT_SECRET")}

	dbg := flag.Bool("debug", false, "Enable debug mode")
	flag.Parse()
	if *dbg {
		os.Remove(path)
	}

	db, err = database.NewDB(path)
	if err != nil {
		log.Fatal(err)
	}

	serverHandler := http.NewServeMux()
	serverHandler.Handle("/app/*", http.StripPrefix("/app", middlewareLog(config.middlewareMetricsInc(http.FileServer(http.Dir("."))))))
	serverHandler.HandleFunc("GET /admin/metrics", config.metrics)
	serverHandler.HandleFunc("GET /api/reset", config.reset)
	serverHandler.HandleFunc("GET /api/healthz", status)
	serverHandler.HandleFunc("GET /api/chirps", getChirps)
	serverHandler.HandleFunc("POST /api/chirps", postChirps)
	serverHandler.HandleFunc("GET /api/chirps/{chirpID}", getChirp)
	serverHandler.HandleFunc("POST /api/users", postUsers)
	serverHandler.HandleFunc("POST /api/login", postLogin)
	serverHandler.HandleFunc("PUT /api/users", updateUser)

	server := http.Server{Handler: serverHandler, Addr: ":" + port}

	fmt.Print("starting server\n")
	fmt.Printf("servering files from: %s. on port: %s\n", filepathRoot, port)
	server.ListenAndServe()
}
