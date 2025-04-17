package main

import (
	"html/template"
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

type Recipe struct {
	ID          int
	Title       string
	Description string
	Time        string
}

var recipes = []Recipe{
	{ID: 1, Title: "Shin Ramen", Description: "Your favorite ramen...but upgraded", Time: "10 minutes"},
	{ID: 2, Title: "Shrimp Dumplings", Description: "Classic dumplings without the carbs", Time: "15 minutes"},
}

func main() {
	r := mux.NewRouter()

	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	r.HandleFunc("/", homeHandler)
	r.HandleFunc("/recipes", listRecipesHandler)
	r.HandleFunc("/recipes/{id}", showRecipeHandler)

	log.Println("Server started at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", r))
}

func renderTemplate(w http.ResponseWriter, tmpl string, data interface{}) {
	t, err := template.ParseFiles(
		"templates/base.html",
		"templates/"+tmpl+".html",
	)
	if err != nil {
		http.Error(w, "Error loading template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	err = t.ExecuteTemplate(w, "base", data)
	if err != nil {
		http.Error(w, "Error executing template: "+err.Error(), http.StatusInternalServerError)
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "home", nil)
}

func listRecipesHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "recipes", recipes)
}

func showRecipeHandler(w http.ResponseWriter, r *http.Request) {
	idStr := mux.Vars(r)["id"]
	id, _ := strconv.Atoi(idStr)
	for _, recipe := range recipes {
		if recipe.ID == id {
			renderTemplate(w, "recipe_detail", recipe)
			return
		}
	}
	http.NotFound(w, r)
}
