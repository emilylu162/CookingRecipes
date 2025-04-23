package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

type ViewData struct {
	CurrentUser bool
	Payload     interface{}
}

// renderTemplateWithUser loads base+tmpl and injects session state automatically.
func renderTemplateWithUser(w http.ResponseWriter, r *http.Request, tmpl string, payload interface{}) {
	//parse templates
	t := template.Must(template.ParseFiles(
		"templates/base.html",
		"templates/"+tmpl+".html",
	))

	//check session
	sess, _ := sessionStore.Get(r, "session")
	_, loggedIn := sess.Values["user_id"].(int)

	//render
	vd := ViewData{CurrentUser: loggedIn, Payload: payload}
	if err := t.ExecuteTemplate(w, "base", vd); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

var (
	db           *sql.DB
	sessionStore = sessions.NewCookieStore([]byte(os.Getenv("SESSION_KEY")))
)

type Recipe struct {
	ID          int
	Title       string
	Description string
	Time        string
	ImagePath   string
}

func main() {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL is not set")
	}
	var err error
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("DB connect:", err)
	}
	defer db.Close()
	if err = db.Ping(); err != nil {
		log.Fatal("DB ping:", err)
	}

	// Router + static handlers
	r := mux.NewRouter()

	// static files
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	r.PathPrefix("/uploads/").Handler(http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))))

	// public pages
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sess, _ := sessionStore.Get(r, "session")
		if sess.Values["user_id"] == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/recipes", http.StatusSeeOther)
		}
	})
	r.HandleFunc("/login", loginHandler).Methods("GET", "POST")
	r.HandleFunc("/signup", signupHandler).Methods("GET", "POST")
	r.HandleFunc("/logout", logoutHandler)

	// recipe list
	r.HandleFunc("/recipes", listRecipesHandler)
	r.Handle("/recipes/new", requireLogin(http.HandlerFunc(newRecipeFormHandler))).Methods("GET")
	r.Handle("/recipes/new", requireLogin(http.HandlerFunc(createRecipeHandler))).Methods("POST")
	r.Handle("/recipes/{id}/edit", requireLogin(http.HandlerFunc(editRecipeFormHandler))).Methods("GET")
	r.Handle("/recipes/{id}/edit", requireLogin(http.HandlerFunc(updateRecipeHandler))).Methods("POST")
	r.HandleFunc("/recipes/{id}", showRecipeHandler)

	log.Println("Server started at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", r))
}

func renderTemplate(w http.ResponseWriter, tmpl string, data interface{}) {
	t, err := template.ParseFiles("templates/base.html", "templates/"+tmpl+".html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplateWithUser(w, r, "home", nil)
}

func listRecipesHandler(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionStore.Get(r, "session")
	userID, ok := sess.Values["user_id"].(int)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	rows, err := db.Query(`
        SELECT id, title, description, time, image_path
          FROM recipes
         WHERE user_id = $1
      ORDER BY id`, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var recs []Recipe
	for rows.Next() {
		var rc Recipe
		var img sql.NullString
		if err := rows.Scan(&rc.ID, &rc.Title, &rc.Description, &rc.Time, &img); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if img.Valid {
			rc.ImagePath = img.String
		}
		recs = append(recs, rc)
	}
	renderTemplateWithUser(w, r, "recipes", recs)
}

func showRecipeHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	var rc Recipe
	var img sql.NullString
	err := db.QueryRow(
		`SELECT title, description, time, image_path FROM recipes WHERE id=$1`, id,
	).Scan(&rc.Title, &rc.Description, &rc.Time, &img)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if img.Valid {
		rc.ImagePath = img.String
	}
	rc.ID = id
	renderTemplateWithUser(w, r, "recipe_detail", rc)
}

func newRecipeFormHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplateWithUser(w, r, "recipe_form", nil)
}

func createRecipeHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	title := r.FormValue("title")
	timeStr := r.FormValue("time")
	desc := r.FormValue("description")

	// Insert without image to get ID
	var newID int
	sess, _ := sessionStore.Get(r, "session")
	userID := sess.Values["user_id"].(int)
	err := db.QueryRow(
		`INSERT INTO recipes (title, description, time, user_id)
         VALUES ($1,$2,$3,$4) RETURNING id`,
		title, desc, timeStr, userID,
	).Scan(&newID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Handle optional image
	file, header, ferr := r.FormFile("image")
	if ferr == nil {
		defer file.Close()
		os.MkdirAll("uploads/images", 0755)
		fn := fmt.Sprintf("%d_%s", newID, header.Filename)
		disk := filepath.Join("uploads", "images", fn)
		url := "/uploads/images/" + fn
		dst, _ := os.Create(disk)
		defer dst.Close()
		io.Copy(dst, file)

		// update row with image_path
		if _, err := db.Exec(
			`UPDATE recipes SET image_path=$1 WHERE id=$2`, url, newID,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if ferr != http.ErrMissingFile {
		http.Error(w, ferr.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/recipes/%d", newID), http.StatusSeeOther)
}

func editRecipeFormHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	var rc Recipe
	var img sql.NullString
	if err := db.QueryRow(
		`SELECT title, description, time, image_path FROM recipes WHERE id=$1`, id,
	).Scan(&rc.Title, &rc.Description, &rc.Time, &img); err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if img.Valid {
		rc.ImagePath = img.String
	}
	rc.ID = id
	renderTemplateWithUser(w, r, "recipe_edit", rc)
}

func updateRecipeHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(mux.Vars(r)["id"])
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	title := r.FormValue("title")
	timeStr := r.FormValue("time")
	desc := r.FormValue("description")

	// optional new image
	imagePath := ""
	file, header, ferr := r.FormFile("image")
	if ferr == nil {
		defer file.Close()
		os.MkdirAll("uploads/images", 0755)
		fn := fmt.Sprintf("%d_%s", id, header.Filename)
		disk := filepath.Join("uploads", "images", fn)
		url := "/uploads/images/" + fn
		dst, _ := os.Create(disk)
		defer dst.Close()
		io.Copy(dst, file)
		imagePath = url
	} else if ferr != http.ErrMissingFile {
		http.Error(w, ferr.Error(), http.StatusBadRequest)
		return
	}

	// conditional UPDATE
	if imagePath != "" {
		_, err := db.Exec(
			`UPDATE recipes
               SET title=$1, description=$2, time=$3, image_path=$4
             WHERE id=$5`,
			title, desc, timeStr, imagePath, id,
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		_, err := db.Exec(
			`UPDATE recipes
               SET title=$1, description=$2, time=$3
             WHERE id=$4`,
			title, desc, timeStr, id,
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, fmt.Sprintf("/recipes/%d", id), http.StatusSeeOther)
}

func signupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		renderTemplateWithUser(w, r, "signup", nil)
		return
	}
	r.ParseForm()
	user := r.FormValue("username")
	pass := r.FormValue("password")

	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := db.Exec(
		`INSERT INTO users(username,password_hash) VALUES($1,$2)`,
		user, hash,
	); err != nil {
		http.Error(w, "Username taken", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		renderTemplateWithUser(w, r, "login", nil)
		return
	}
	r.ParseForm()
	user := r.FormValue("username")
	pass := r.FormValue("password")

	var id int
	var hash string
	err := db.QueryRow(
		`SELECT id,password_hash FROM users WHERE username=$1`, user,
	).Scan(&id, &hash)
	if err == sql.ErrNoRows || bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	sess, _ := sessionStore.Get(r, "session")
	sess.Values["user_id"] = id
	sess.Save(r, w)
	http.Redirect(w, r, "/recipes", http.StatusSeeOther)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionStore.Get(r, "session")
	sess.Options.MaxAge = -1
	sess.Save(r, w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, _ := sessionStore.Get(r, "session")
		if sess.Values["user_id"] == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
